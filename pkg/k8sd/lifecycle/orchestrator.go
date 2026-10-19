package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/canonical/k8sd/pkg/log"
)

// Journal persists nodeStatus and lease. It must be linearizable; in k8sd it
// is backed by dqlite (see SQLJournal).
type Journal interface {
	Load(ctx context.Context) (JournalState, error)
	// CompareAndSwap applies t atomically, or returns ErrConflict if the
	// current lease or phase do not match t.FromLease / t.FromPhase.
	CompareAndSwap(ctx context.Context, t Transition) error
}

// Membership is the microcluster (dqlite) membership. Remove must let
// dqlite hand a voter's role to an online spare before the member leaves.
type Membership interface {
	Members(ctx context.Context) (map[string]MemberInfo, error)
	Remove(ctx context.Context, node string) error
}

// Datastore is the etcd cluster membership API.
type Datastore interface {
	Members(ctx context.Context) ([]EtcdMember, error)
	// AddLearner adds a learner. The member ID is discovered by observation
	// rather than from the response, which may be lost after commit.
	AddLearner(ctx context.Context, peerURL string) error
	Promote(ctx context.Context, id uint64) error
	Remove(ctx context.Context, id uint64) error
}

// Hosts controls node-local runtime state. StartEtcd and StopEtcd must run
// on the target node's own k8sd (e.g. through an endpoint on that node), and
// fail when that host is down.
type Hosts interface {
	// Status reports Up=false (not an error) for unreachable hosts.
	Status(ctx context.Context, node string) (HostStatus, error)
	StartEtcd(ctx context.Context, node string) error
	StopEtcd(ctx context.Context, node string) error
}

// Orchestrator drives nodes through the lifecycle of NodeLifecycle.tla.
// It is safe for concurrent use.
type Orchestrator struct {
	journal Journal
	members Membership
	etcd    Datastore
	hosts   Hosts

	activate        func(ctx context.Context, node string) error
	retryInterval   time.Duration
	teardownTimeout time.Duration

	// mu serialises observe+execute so concurrent callers in this process
	// never act on the same observation. Cross-process exclusion is the
	// journal lease.
	mu sync.Mutex
}

// Option configures an Orchestrator.
type Option func(*Orchestrator)

// WithActivationCheck runs f right before a joining node is marked Active,
// e.g. to start services and wait for kube-apiserver. Errors are retried
// unless wrapped with Permanent.
func WithActivationCheck(f func(ctx context.Context, node string) error) Option {
	return func(o *Orchestrator) { o.activate = f }
}

// WithRetryInterval sets the delay between attempts of a blocked step.
func WithRetryInterval(d time.Duration) Option {
	return func(o *Orchestrator) { o.retryInterval = d }
}

// WithTeardownTimeout bounds the rollback that Join performs after a failure.
func WithTeardownTimeout(d time.Duration) Option {
	return func(o *Orchestrator) { o.teardownTimeout = d }
}

// New returns an Orchestrator.
func New(journal Journal, members Membership, etcd Datastore, hosts Hosts, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		journal:         journal,
		members:         members,
		etcd:            etcd,
		hosts:           hosts,
		retryInterval:   2 * time.Second,
		teardownTimeout: 2 * time.Minute,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Observe builds a Snapshot from all four sources.
func (o *Orchestrator) Observe(ctx context.Context) (Snapshot, error) {
	js, err := o.journal.Load(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to load journal: %w", err)
	}
	members, err := o.members.Members(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to list cluster members: %w", err)
	}
	etcdMembers, err := o.etcd.Members(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to list etcd members: %w", err)
	}

	nodes := map[string]NodeView{}
	view := func(name string) NodeView {
		if v, ok := nodes[name]; ok {
			return v
		}
		return NodeView{Name: name, Phase: PhaseAbsent, Member: MemberNone, Dqlite: DqliteNone, Etcd: EtcdNone}
	}
	for name, rec := range js.Records {
		v := view(name)
		v.PeerURL, v.Phase = rec.PeerURL, rec.Phase
		nodes[name] = v
	}
	for name, m := range members {
		v := view(name)
		v.Member, v.Dqlite = m.Role, m.Dqlite
		if v.Dqlite == "" {
			v.Dqlite = DqliteNone
		}
		nodes[name] = v
	}

	byPeerURL := map[string]string{}
	for name, v := range nodes {
		if v.PeerURL != "" {
			byPeerURL[v.PeerURL] = name
		}
	}

	var orphans []EtcdMember
	for _, m := range etcdMembers {
		name := m.Name
		if _, known := nodes[name]; name == "" || !known {
			for _, u := range m.PeerURLs {
				if n, ok := byPeerURL[u]; ok {
					name = n
					break
				}
			}
		}
		if name == "" || view(name).Etcd != EtcdNone {
			orphans = append(orphans, m)
			continue
		}
		v := view(name)
		v.EtcdMemberID = m.ID
		v.Etcd = EtcdVoter
		if m.IsLearner {
			v.Etcd = EtcdLearner
		}
		nodes[name] = v
	}

	for name, v := range nodes {
		st, err := o.hosts.Status(ctx, name)
		if err != nil {
			return Snapshot{}, fmt.Errorf("failed to get status of host %s: %w", name, err)
		}
		v.Up = st.Up
		v.EtcdRunning = st.Up && st.EtcdRunning
		nodes[name] = v
	}

	return Snapshot{Lease: js.Lease, Nodes: nodes, Orphans: orphans}, nil
}

// BeginJoin acquires the lease for node and records it as Joining. As in
// microcluster's PostJoin hook, node must already be a microcluster member.
// It is idempotent for a join that is already in flight for node.
func (o *Orchestrator) BeginJoin(ctx context.Context, node, peerURL string) error {
	if node == "" || peerURL == "" {
		return Permanent(errors.New("node name and etcd peer URL are required"))
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	s, err := o.Observe(ctx)
	if err != nil {
		return err
	}
	v := s.Node(node)
	switch {
	case v.Phase == PhaseJoining && s.Lease == node:
		return nil
	case v.Phase != PhaseAbsent:
		return fmt.Errorf("%w: node %s is %s", ErrInvalidPhase, node, v.Phase)
	case v.Etcd != EtcdNone || v.EtcdRunning:
		return fmt.Errorf("%w: node %s has stale etcd state (role=%s running=%t), remove it first", ErrInvalidPhase, node, v.Etcd, v.EtcdRunning)
	case v.Member != MemberJoined:
		return fmt.Errorf("%w: node %s is microcluster member=%s", ErrAwaitingMember, node, v.Member)
	case s.Lease != "":
		return fmt.Errorf("%w: lease held by %s", ErrLeaseHeld, s.Lease)
	case !s.DqliteQuorum():
		return ErrNoDqliteQuorum
	}
	for _, m := range s.Orphans {
		if slices.Contains(m.PeerURLs, peerURL) {
			return fmt.Errorf("%w: etcd member %x already uses peer URL %s, remove it first", ErrInvalidPhase, m.ID, peerURL)
		}
		if m.IsLearner {
			return fmt.Errorf("%w: orphaned etcd learner %x (%v) must be removed first", ErrLearnerBusy, m.ID, m.PeerURLs)
		}
	}
	return o.journal.CompareAndSwap(ctx, Transition{
		Node: node, PeerURL: peerURL,
		FromPhase: PhaseAbsent, ToPhase: PhaseJoining,
		FromLease: "", ToLease: node,
	})
}

// Abort moves a Joining node to RollingBack (FailJoin). Idempotent.
func (o *Orchestrator) Abort(ctx context.Context, node string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	s, err := o.Observe(ctx)
	if err != nil {
		return err
	}
	v := s.Node(node)
	switch {
	case v.Phase == PhaseRollingBack:
		return nil
	case v.Phase != PhaseJoining:
		return fmt.Errorf("%w: node %s is %s", ErrInvalidPhase, node, v.Phase)
	case !s.DqliteQuorum():
		return ErrNoDqliteQuorum
	}
	return o.journal.CompareAndSwap(ctx, Transition{
		Node:      node,
		FromPhase: PhaseJoining, ToPhase: PhaseRollingBack,
		FromLease: node, ToLease: node,
	})
}

// BeginRemove acquires the lease for node and records it as Removing.
// It is idempotent for a removal that is already in flight for node.
func (o *Orchestrator) BeginRemove(ctx context.Context, node string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	s, err := o.Observe(ctx)
	if err != nil {
		return err
	}
	v := s.Node(node)
	switch {
	case v.Phase == PhaseRemoving && s.Lease == node:
		return nil
	case v.Phase != PhaseActive:
		return fmt.Errorf("%w: node %s is %s", ErrInvalidPhase, node, v.Phase)
	case s.Lease != "":
		return fmt.Errorf("%w: lease held by %s", ErrLeaseHeld, s.Lease)
	case !s.DqliteQuorum():
		return ErrNoDqliteQuorum
	case len(s.Voters()) <= 1:
		return ErrLastControlPlane
	}
	return o.journal.CompareAndSwap(ctx, Transition{
		Node:      node,
		FromPhase: PhaseActive, ToPhase: PhaseRemoving,
		FromLease: "", ToLease: node,
	})
}

// Step observes the cluster and applies at most one step for node.
func (o *Orchestrator) Step(ctx context.Context, node string) (Step, error) {
	step, _, err := o.step(ctx, node)
	return step, err
}

func (o *Orchestrator) step(ctx context.Context, node string) (Step, Phase, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	s, err := o.Observe(ctx)
	if err != nil {
		return StepNone, "", err
	}
	v := s.Node(node)
	step, err := Plan(s, node)
	if err != nil || step == StepNone {
		return step, v.Phase, err
	}
	if err := o.execute(ctx, v, step); err != nil {
		return step, v.Phase, fmt.Errorf("step %s for node %s failed: %w", step, node, err)
	}
	return step, v.Phase, nil
}

func (o *Orchestrator) execute(ctx context.Context, v NodeView, step Step) error {
	switch step {
	case StepAddLearner:
		return o.etcd.AddLearner(ctx, v.PeerURL)
	case StepStartEtcd:
		return o.hosts.StartEtcd(ctx, v.Name)
	case StepPromote:
		return o.etcd.Promote(ctx, v.EtcdMemberID)
	case StepActivate:
		if o.activate != nil {
			if err := o.activate(ctx, v.Name); err != nil {
				return fmt.Errorf("activation check failed: %w", err)
			}
		}
		return o.journal.CompareAndSwap(ctx, Transition{
			Node:      v.Name,
			FromPhase: PhaseJoining, ToPhase: PhaseActive,
			FromLease: v.Name, ToLease: "",
		})
	case StepRemoveVoter, StepRemoveLearner:
		return o.etcd.Remove(ctx, v.EtcdMemberID)
	case StepStopEtcd:
		return o.hosts.StopEtcd(ctx, v.Name)
	case StepLeaveCluster:
		return o.members.Remove(ctx, v.Name)
	case StepFinishTeardown:
		return o.journal.CompareAndSwap(ctx, Transition{
			Node:      v.Name,
			FromPhase: v.Phase, ToPhase: PhaseAbsent,
			FromLease: v.Name, ToLease: "",
		})
	case StepNone:
		return nil
	default:
		return fmt.Errorf("unknown step %q", step)
	}
}

// Reconcile applies steps for node until it reaches a stable phase (Active
// or Absent), a permanent error occurs, or ctx is done. Blocked and failed
// steps are retried: every step is idempotent because it is re-planned from
// a fresh observation.
func (o *Orchestrator) Reconcile(ctx context.Context, node string) (Phase, error) {
	logger := log.FromContext(ctx).WithValues("component", "lifecycle", "node", node)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return "", fmt.Errorf("%w (last error: %w)", err, lastErr)
			}
			return "", err
		}
		step, phase, err := o.step(ctx, node)
		switch {
		case err == nil && step == StepNone:
			return phase, nil
		case err == nil:
			lastErr = nil
			logger.V(1).Info("Applied lifecycle step", "step", step, "phase", phase)
			continue
		case IsPermanent(err), errors.Is(err, ErrLeaseLost):
			return phase, err
		}
		lastErr = err

		logger.V(1).Info("Lifecycle step not applied, will retry", "step", step, "phase", phase, "reason", err.Error())
		timer := time.NewTimer(o.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return phase, fmt.Errorf("%w (last error: %w)", ctx.Err(), err)
		case <-timer.C:
		}
	}
}

// Join drives node from Absent to Active. On failure it rolls back with a
// context detached from ctx, never stopping etcd on a voter and never
// removing a voter if that would lose quorum. If the rollback cannot finish,
// the journal keeps the lease and Resume completes it later.
func (o *Orchestrator) Join(ctx context.Context, node, peerURL string) error {
	if err := o.BeginJoin(ctx, node, peerURL); err != nil {
		return fmt.Errorf("failed to begin join of %s: %w", node, err)
	}
	phase, err := o.Reconcile(ctx, node)
	if err == nil && phase == PhaseActive {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("join of %s ended in phase %s", node, phase)
	}

	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.teardownTimeout)
	defer cancel()
	log.FromContext(ctx).Error(err, "Join failed, rolling back", "node", node)

	if abortErr := o.Abort(rbCtx, node); abortErr != nil && !errors.Is(abortErr, ErrInvalidPhase) {
		return errors.Join(err, fmt.Errorf("failed to abort join of %s, resume required: %w", node, abortErr))
	}
	final, rbErr := o.Reconcile(rbCtx, node)
	if rbErr != nil {
		return errors.Join(err, fmt.Errorf("rollback of %s incomplete, resume required: %w", node, rbErr))
	}
	if final == PhaseActive {
		// Activate committed but its response was lost before the abort.
		return nil
	}
	return err
}

// Remove drives an Active node to Absent. Removal is forward-only: on error
// the journal keeps the lease and Resume completes it later.
func (o *Orchestrator) Remove(ctx context.Context, node string) error {
	if err := o.BeginRemove(ctx, node); err != nil {
		return fmt.Errorf("failed to begin removal of %s: %w", node, err)
	}
	if _, err := o.Reconcile(ctx, node); err != nil {
		return fmt.Errorf("removal of %s incomplete, resume required: %w", node, err)
	}
	return nil
}

// Resume completes the in-flight transition recorded in the journal, if any.
// Call it on daemon start: it recovers from orchestrator crashes and reboots.
func (o *Orchestrator) Resume(ctx context.Context) (string, Phase, error) {
	js, err := o.journal.Load(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to load journal: %w", err)
	}
	if js.Lease == "" {
		return "", "", nil
	}
	phase, err := o.Reconcile(ctx, js.Lease)
	return js.Lease, phase, err
}
