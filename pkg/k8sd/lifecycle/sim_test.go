package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/lifecycle"
)

var (
	errDqliteNoLeader = errors.New("dqlite: no leader")
	errEtcdNoLeader   = errors.New("etcdserver: no leader")
	errHostDown       = errors.New("host unreachable")
	errInjected       = errors.New("injected failure")
)

func peerURL(node string) string { return "https://" + node + ":2380" }

type simMember struct {
	id      uint64
	node    string
	name    string // set once the member's etcd has started
	learner bool
}

// fault is consumed by the next matching op. commit=true applies the op
// before returning err, i.e. the response is lost after commit.
type fault struct {
	commit bool
	err    error
	then   func(s *sim) // runs with s.mu held after the op
}

// sim is a ground-truth model of journal + microcluster + etcd + hosts with
// the same guards as NodeLifecycle.tla. It records every violation of the
// spec's invariants and action properties.
type sim struct {
	mu sync.Mutex

	universe []string
	lease    string
	records  map[string]lifecycle.Record
	mc       map[string]lifecycle.MemberRole
	members  map[uint64]*simMember
	nextID   uint64
	up       map[string]bool
	running  map[string]bool

	faults     map[string][]fault
	ops        []string
	violations []string
}

func newSim(nodes ...string) *sim {
	s := &sim{
		universe: nodes,
		records:  map[string]lifecycle.Record{},
		mc:       map[string]lifecycle.MemberRole{},
		members:  map[uint64]*simMember{},
		up:       map[string]bool{},
		running:  map[string]bool{},
		faults:   map[string][]fault{},
	}
	for _, n := range nodes {
		s.up[n] = true
	}
	s.seedActive(nodes[0])
	return s
}

// seedActive installs n as an Active control-plane node without the orchestrator.
func (s *sim) seedActive(n string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[n] = lifecycle.Record{Name: n, PeerURL: peerURL(n), Phase: lifecycle.PhaseActive}
	s.mc[n] = lifecycle.MemberJoined
	s.nextID++
	s.members[s.nextID] = &simMember{id: s.nextID, node: n, name: n}
	s.running[n] = true
	if err := s.truthLocked().CheckInvariants(); err != nil {
		panic(err)
	}
}

func (s *sim) memberOf(n string) *simMember {
	for _, m := range s.members {
		if m.node == n {
			return m
		}
	}
	return nil
}

func (s *sim) truthLocked() lifecycle.Snapshot {
	nodes := map[string]lifecycle.NodeView{}
	for _, n := range s.universe {
		v := lifecycle.NodeView{Name: n, Phase: lifecycle.PhaseAbsent, Member: lifecycle.MemberNone, Etcd: lifecycle.EtcdNone, Up: s.up[n], EtcdRunning: s.up[n] && s.running[n]}
		if r, ok := s.records[n]; ok {
			v.Phase, v.PeerURL = r.Phase, r.PeerURL
		}
		if r, ok := s.mc[n]; ok {
			v.Member = r
		}
		if m := s.memberOf(n); m != nil {
			v.EtcdMemberID = m.id
			v.Etcd = lifecycle.EtcdVoter
			if m.learner {
				v.Etcd = lifecycle.EtcdLearner
			}
		}
		nodes[n] = v
	}
	return lifecycle.Snapshot{Lease: s.lease, Nodes: nodes}
}

func (s *sim) truth() lifecycle.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.truthLocked()
}

func mcJoined(snap lifecycle.Snapshot) []string {
	var out []string
	for n, v := range snap.Nodes {
		if v.Member == lifecycle.MemberJoined {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// checkLocked evaluates invariants on after and action properties on (before, after).
func (s *sim) checkLocked(op string, before, after lifecycle.Snapshot, orchestrator bool) {
	report := func(format string, args ...any) {
		s.violations = append(s.violations, op+": "+fmt.Sprintf(format, args...))
	}
	if err := after.CheckInvariants(); err != nil {
		report("%v", err)
	}
	if !slices.Equal(before.Voters(), after.Voters()) && (!before.EtcdQuorum() || !after.EtcdQuorum()) {
		report("ReconfigPreservesQuorum: etcd voters %v -> %v", before.Voters(), after.Voters())
	}
	if !slices.Equal(mcJoined(before), mcJoined(after)) && (!before.DqliteQuorum() || !after.DqliteQuorum()) {
		report("ReconfigPreservesQuorum: dqlite members %v -> %v", mcJoined(before), mcJoined(after))
	}
	if orchestrator && before.EtcdQuorum() && !after.EtcdQuorum() {
		report("OrchestratorNeverBreaksQuorum")
	}
	for n, v := range after.Nodes {
		if v.Phase == lifecycle.PhaseActive && before.Nodes[n].Phase != lifecycle.PhaseActive && (!before.EtcdQuorum() || !before.DqliteQuorum()) {
			report("ActivationRequiresQuorum: %s", n)
		}
	}
}

func (s *sim) inject(op string, f fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[op] = append(s.faults[op], f)
}

func (s *sim) takeFaultLocked(op string) (fault, bool) {
	for _, key := range []string{op, "*"} {
		if q := s.faults[key]; len(q) > 0 {
			s.faults[key] = q[1:]
			return q[0], true
		}
	}
	return fault{}, false
}

// mutate runs one externally visible op with fault injection and checking.
func (s *sim) mutate(op string, guard func() error, apply func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, faulty := s.takeFaultLocked(op)
	if faulty && !f.commit && f.err != nil {
		return fmt.Errorf("%s: %w", op, f.err)
	}
	if err := guard(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	before := s.truthLocked()
	apply()
	s.ops = append(s.ops, op)
	s.checkLocked(op, before, s.truthLocked(), true)
	if faulty {
		if f.then != nil {
			f.then(s)
		}
		if f.err != nil {
			return fmt.Errorf("%s: %w", op, f.err)
		}
	}
	return nil
}

func (s *sim) crashLocked(n string) {
	before := s.truthLocked()
	s.up[n] = false
	s.running[n] = false
	s.checkLocked("env.Crash("+n+")", before, s.truthLocked(), false)
}

func (s *sim) restartLocked(n string) {
	before := s.truthLocked()
	s.up[n] = true
	// The etcd snap service stays enabled while the host is an etcd member.
	m := s.memberOf(n)
	s.running[n] = m != nil
	if m != nil {
		m.name = n
	}
	s.checkLocked("env.Restart("+n+")", before, s.truthLocked(), false)
}

func (s *sim) crash(n string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashLocked(n)
}

func (s *sim) restart(n string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restartLocked(n)
}

func (s *sim) opsLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ops)
}

func (s *sim) Violations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.violations)
}

// etcdMembersFor counts members attributed to n (ground truth).
func (s *sim) etcdMembersFor(n string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := 0
	for _, m := range s.members {
		if m.node == n {
			c++
		}
	}
	return c
}

func (s *sim) orchestrator(opts ...lifecycle.Option) *lifecycle.Orchestrator {
	opts = append([]lifecycle.Option{
		lifecycle.WithRetryInterval(time.Millisecond),
		lifecycle.WithTeardownTimeout(2 * time.Second),
	}, opts...)
	return lifecycle.New(simJournal{s}, simMembership{s}, simEtcd{s}, simHosts{s}, opts...)
}

// ---- Journal (dqlite) ----

type simJournal struct{ s *sim }

func (j simJournal) Load(context.Context) (lifecycle.JournalState, error) {
	s := j.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.truthLocked().DqliteQuorum() {
		return lifecycle.JournalState{}, errDqliteNoLeader
	}
	js := lifecycle.JournalState{Lease: s.lease, Records: map[string]lifecycle.Record{}}
	for k, v := range s.records {
		js.Records[k] = v
	}
	return js, nil
}

func (j simJournal) CompareAndSwap(_ context.Context, t lifecycle.Transition) error {
	s := j.s
	op := fmt.Sprintf("journal.CAS(%s:%s->%s)", t.Node, t.FromPhase, t.ToPhase)
	return s.mutate(op, func() error {
		if !s.truthLocked().DqliteQuorum() {
			return errDqliteNoLeader
		}
		phase := lifecycle.PhaseAbsent
		if r, ok := s.records[t.Node]; ok {
			phase = r.Phase
		}
		if s.lease != t.FromLease || phase != t.FromPhase {
			return fmt.Errorf("%w: lease=%q phase=%s", lifecycle.ErrConflict, s.lease, phase)
		}
		return nil
	}, func() {
		if t.ToPhase == lifecycle.PhaseAbsent {
			delete(s.records, t.Node)
		} else {
			r := s.records[t.Node]
			if r.PeerURL == "" {
				r = lifecycle.Record{Name: t.Node, PeerURL: t.PeerURL}
			}
			r.Phase = t.ToPhase
			s.records[t.Node] = r
		}
		s.lease = t.ToLease
		// BeginJoin also submits the microcluster join request (mcRole := pending).
		if t.FromPhase == lifecycle.PhaseAbsent && t.ToPhase == lifecycle.PhaseJoining {
			s.mc[t.Node] = lifecycle.MemberPending
		}
	})
}

// ---- Membership (microcluster) ----

type simMembership struct{ s *sim }

func (m simMembership) Members(context.Context) (map[string]lifecycle.MemberRole, error) {
	s := m.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.truthLocked().DqliteQuorum() {
		return nil, errDqliteNoLeader
	}
	// ConfirmMember: microcluster completes pending joins of live hosts.
	for _, n := range s.universe {
		if s.mc[n] == lifecycle.MemberPending && s.up[n] {
			before := s.truthLocked()
			s.mc[n] = lifecycle.MemberJoined
			s.checkLocked("env.ConfirmMember("+n+")", before, s.truthLocked(), false)
		}
	}
	out := map[string]lifecycle.MemberRole{}
	for k, v := range s.mc {
		out[k] = v
	}
	return out, nil
}

func (m simMembership) Remove(_ context.Context, node string) error {
	s := m.s
	return s.mutate("members.Remove("+node+")", func() error {
		t := s.truthLocked()
		if !t.DqliteQuorum() {
			return errDqliteNoLeader
		}
		if _, ok := s.mc[node]; !ok {
			return fmt.Errorf("member %s not found", node)
		}
		if s.mc[node] == lifecycle.MemberJoined && !t.DqliteQuorumWithout(node) {
			return errors.New("dqlite: removal would lose quorum")
		}
		return nil
	}, func() { delete(s.mc, node) })
}

// ---- Datastore (etcd) ----

type simEtcd struct{ s *sim }

func (e simEtcd) Members(context.Context) ([]lifecycle.EtcdMember, error) {
	s := e.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.truthLocked().EtcdQuorum() {
		return nil, errEtcdNoLeader
	}
	var out []lifecycle.EtcdMember
	for _, m := range s.members {
		out = append(out, lifecycle.EtcdMember{ID: m.id, Name: m.name, PeerURLs: []string{peerURL(m.node)}, IsLearner: m.learner})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (e simEtcd) AddLearner(_ context.Context, url string) error {
	s := e.s
	node := strings.TrimSuffix(strings.TrimPrefix(url, "https://"), ":2380")
	return s.mutate("etcd.AddLearner("+node+")", func() error {
		if !s.truthLocked().EtcdQuorum() {
			return errEtcdNoLeader
		}
		for _, m := range s.members {
			if m.learner {
				return errors.New("etcdserver: too many learner members in cluster")
			}
			if m.node == node {
				return errors.New("etcdserver: Peer URLs already exists")
			}
		}
		return nil
	}, func() {
		s.nextID++
		s.members[s.nextID] = &simMember{id: s.nextID, node: node, learner: true}
	})
}

func (e simEtcd) nodeOf(id uint64) string {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	if m, ok := e.s.members[id]; ok {
		return m.node
	}
	return fmt.Sprintf("id-%d", id)
}

func (e simEtcd) Promote(_ context.Context, id uint64) error {
	s := e.s
	return s.mutate("etcd.Promote("+e.nodeOf(id)+")", func() error {
		m, ok := s.members[id]
		switch {
		case !s.truthLocked().EtcdQuorum():
			return errEtcdNoLeader
		case !ok:
			return errors.New("etcdserver: member not found")
		case !m.learner:
			return errors.New("etcdserver: can only promote a learner member")
		case !s.up[m.node] || !s.running[m.node]:
			return errors.New("etcdserver: can only promote a learner member which is in sync with leader")
		}
		return nil
	}, func() { s.members[id].learner = false })
}

func (e simEtcd) Remove(_ context.Context, id uint64) error {
	s := e.s
	return s.mutate("etcd.Remove("+e.nodeOf(id)+")", func() error {
		t := s.truthLocked()
		m, ok := s.members[id]
		switch {
		case !t.EtcdQuorum():
			return errEtcdNoLeader
		case !ok:
			return errors.New("etcdserver: member not found")
		case !m.learner && !t.EtcdQuorumWithout(m.node):
			// etcd --strict-reconfig-check
			return errors.New("etcdserver: re-configuration failed due to not enough started members")
		}
		return nil
	}, func() { delete(s.members, id) })
}

// ---- Hosts ----

type simHosts struct{ s *sim }

func (h simHosts) Status(_ context.Context, node string) (lifecycle.HostStatus, error) {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	return lifecycle.HostStatus{Up: h.s.up[node], EtcdRunning: h.s.up[node] && h.s.running[node]}, nil
}

func (h simHosts) StartEtcd(_ context.Context, node string) error {
	s := h.s
	return s.mutate("hosts.StartEtcd("+node+")", func() error {
		if !s.up[node] {
			return errHostDown
		}
		return nil
	}, func() {
		s.running[node] = true
		if m := s.memberOf(node); m != nil {
			m.name = node
		}
	})
}

func (h simHosts) StopEtcd(_ context.Context, node string) error {
	s := h.s
	return s.mutate("hosts.StopEtcd("+node+")", func() error {
		if !s.up[node] {
			return errHostDown
		}
		return nil
	}, func() { s.running[node] = false })
}

// ---- helpers ----

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// stepUntil applies steps for node until want is applied, failing on error.
func stepUntil(t *testing.T, ctx context.Context, o *lifecycle.Orchestrator, node string, want lifecycle.Step) {
	t.Helper()
	for i := 0; i < 20; i++ {
		step, err := o.Step(ctx, node)
		if err != nil {
			t.Fatalf("step %s for %s: %v", step, node, err)
		}
		if step == want {
			return
		}
		if step == lifecycle.StepNone {
			t.Fatalf("node %s reached a stable phase before step %s", node, want)
		}
	}
	t.Fatalf("step %s for %s not reached", want, node)
}
