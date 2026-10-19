package lifecycle

import (
	"errors"
	"fmt"
	"sort"
)

// NodeView is the observed value of every per-node TLA+ variable.
type NodeView struct {
	Name         string
	PeerURL      string
	Phase        Phase      // nodeStatus[n]
	Member       MemberRole // mcRole[n]
	Dqlite       DqliteRole // dqliteRole[n]
	Etcd         EtcdRole   // etcdRole[n]
	EtcdMemberID uint64
	EtcdRunning  bool // etcdRunning[n]; always false when !Up
	Up           bool // up[n]
}

// Snapshot is one observation of the cluster, i.e. one TLA+ state.
type Snapshot struct {
	// Lease is the node owning the in-flight transition, "" for NoNode.
	Lease string
	Nodes map[string]NodeView
	// Orphans are etcd members that cannot be attributed to any node, e.g. a
	// learner left behind by a join whose MemberAdd response was lost. They
	// count toward etcd's learner limit and quorum size, and are never live.
	Orphans []EtcdMember
}

// Node returns the view of name, or an Absent view if it is unknown.
func (s Snapshot) Node(name string) NodeView {
	if v, ok := s.Nodes[name]; ok {
		return v
	}
	return NodeView{Name: name, Phase: PhaseAbsent, Member: MemberNone, Dqlite: DqliteNone, Etcd: EtcdNone}
}

func (s Snapshot) names() []string {
	names := make([]string, 0, len(s.Nodes))
	for n := range s.Nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func majority(live, all int) bool { return 2*live > all }

func (s Snapshot) etcdCounts(exclude string) (live, all int) {
	for name, v := range s.Nodes {
		if name == exclude || v.Etcd != EtcdVoter {
			continue
		}
		all++
		if v.Up && v.EtcdRunning {
			live++
		}
	}
	for _, m := range s.Orphans {
		if !m.IsLearner {
			all++
		}
	}
	return live, all
}

func (s Snapshot) dqliteCounts(exclude string) (live, all int) {
	for name, v := range s.Nodes {
		if name == exclude || v.Dqlite != DqliteVoter {
			continue
		}
		all++
		if v.Up {
			live++
		}
	}
	return live, all
}

// EtcdQuorum is EtcdQuorum in the spec.
func (s Snapshot) EtcdQuorum() bool { return majority(s.etcdCounts("")) }

// EtcdQuorumWithout is EtcdQuorumWithout(n): quorum after removing n.
func (s Snapshot) EtcdQuorumWithout(n string) bool {
	live, all := s.etcdCounts(n)
	return all > 0 && majority(live, all)
}

// DqliteQuorum is DqliteQuorum in the spec.
func (s Snapshot) DqliteQuorum() bool { return majority(s.dqliteCounts("")) }

// DqliteQuorumWithout is DqliteQuorumWithout(n).
func (s Snapshot) DqliteQuorumWithout(n string) bool {
	live, all := s.dqliteCounts(n)
	return all > 0 && majority(live, all)
}

// CanLeaveDqlite reports whether node n can leave dqlite without losing
// quorum: it is not a voter, quorum survives without it, or it can hand its
// vote to an online spare first (go-dqlite handover). This is DqliteLeave(n).
func (s Snapshot) CanLeaveDqlite(n string) bool {
	if s.Node(n).Dqlite != DqliteVoter || s.DqliteQuorumWithout(n) {
		return true
	}
	for name, v := range s.Nodes {
		if name != n && v.Dqlite == DqliteSpare && v.Up {
			return true
		}
	}
	return false
}

// DqliteVoters returns the dqlite voters, sorted.
func (s Snapshot) DqliteVoters() []string {
	var out []string
	for _, n := range s.names() {
		if s.Nodes[n].Dqlite == DqliteVoter {
			out = append(out, n)
		}
	}
	return out
}

// Voters returns the attributed etcd voters, sorted.
func (s Snapshot) Voters() []string {
	var out []string
	for _, n := range s.names() {
		if s.Nodes[n].Etcd == EtcdVoter {
			out = append(out, n)
		}
	}
	return out
}

// LearnerCount includes orphaned learners.
func (s Snapshot) LearnerCount() int {
	c := 0
	for _, v := range s.Nodes {
		if v.Etcd == EtcdLearner {
			c++
		}
	}
	for _, m := range s.Orphans {
		if m.IsLearner {
			c++
		}
	}
	return c
}

// CheckInvariants evaluates the state invariants of NodeLifecycle.tla.
func (s Snapshot) CheckInvariants() error {
	var errs []error
	transitional := 0
	for _, n := range s.names() {
		v := s.Nodes[n]
		if !v.Phase.Valid() {
			errs = append(errs, fmt.Errorf("TypeOK: node %s has phase %q", n, v.Phase))
		}
		if v.Phase.Transitional() {
			transitional++
			if s.Lease != n {
				errs = append(errs, fmt.Errorf("LeaseOwnsTransition: node %s is %s but lease is %q", n, v.Phase, s.Lease))
			}
		}
		if v.Etcd == EtcdLearner && s.Lease != n {
			errs = append(errs, fmt.Errorf("LearnerHoldsLease: learner %s does not hold the lease (%q)", n, s.Lease))
		}
		if v.Phase == PhaseActive && (v.Etcd != EtcdVoter || v.Member != MemberJoined) {
			errs = append(errs, fmt.Errorf("ActiveIsConfirmedVoter: node %s is Active with etcd=%s member=%s", n, v.Etcd, v.Member))
		}
		if v.Phase == PhaseAbsent && (v.Etcd != EtcdNone || v.EtcdRunning) {
			errs = append(errs, fmt.Errorf("AbsentIsClean: node %s is Absent with etcd=%s running=%t", n, v.Etcd, v.EtcdRunning))
		}
		if v.Etcd != EtcdNone && v.Member != MemberJoined {
			errs = append(errs, fmt.Errorf("NoOrphanedEtcdMember: node %s is an etcd %s but microcluster member=%s", n, v.Etcd, v.Member))
		}
		if (v.Dqlite != DqliteNone) != (v.Member == MemberJoined) {
			errs = append(errs, fmt.Errorf("DqliteRoleMatchesMembership: node %s has dqlite=%s member=%s", n, v.Dqlite, v.Member))
		}
	}
	if transitional > 1 {
		errs = append(errs, fmt.Errorf("SingleTransition: %d nodes are in transition", transitional))
	}
	if c := s.LearnerCount(); c > 1 {
		errs = append(errs, fmt.Errorf("AtMostOneLearner: %d learners", c))
	}
	if _, all := s.etcdCounts(""); all == 0 {
		errs = append(errs, errors.New("ControlPlaneNeverEmpty: no etcd voters"))
	}
	if len(s.DqliteVoters()) == 0 {
		errs = append(errs, errors.New("ControlPlaneNeverEmpty: no dqlite voters"))
	}
	return errors.Join(errs...)
}
