package lifecycle

import "fmt"

// Plan returns the single orchestrator step enabled for node in s, mirroring
// the guards of NodeLifecycle.tla. It returns StepNone for stable phases and
// an ErrBlocked-wrapped error when the node is in transition but no step is
// enabled yet (lost quorum, node down, ...).
func Plan(s Snapshot, node string) (Step, error) {
	v := s.Node(node)
	switch {
	case !v.Phase.Transitional():
		return StepNone, nil
	case s.Lease != node:
		return StepNone, fmt.Errorf("%w: node %s is %s but lease is %q", ErrLeaseLost, node, v.Phase, s.Lease)
	case v.Phase == PhaseJoining:
		return planJoin(s, v)
	default:
		return planTeardown(s, v)
	}
}

func planJoin(s Snapshot, v NodeView) (Step, error) {
	switch {
	case v.Member != MemberJoined:
		if !v.Up {
			return StepNone, ErrNodeDown
		}
		return StepNone, ErrAwaitingMember

	case v.Etcd == EtcdNone:
		if s.LearnerCount() > 0 {
			return StepNone, ErrLearnerBusy
		}
		if !s.EtcdQuorum() {
			return StepNone, ErrNoEtcdQuorum
		}
		return StepAddLearner, nil

	case !v.EtcdRunning:
		if !v.Up {
			return StepNone, ErrNodeDown
		}
		return StepStartEtcd, nil

	case !s.EtcdQuorum():
		return StepNone, ErrNoEtcdQuorum

	case v.Etcd == EtcdLearner:
		return StepPromote, nil

	case !s.DqliteQuorum():
		return StepNone, ErrNoDqliteQuorum

	default:
		return StepActivate, nil
	}
}

// planTeardown orders steps for quorum safety: a voter is removed while its
// local etcd still counts toward quorum, and etcd is only ever stopped on a
// non-voter. Stopping first is the "stop-before-remove" mutation TLC rejects.
func planTeardown(s Snapshot, v NodeView) (Step, error) {
	switch {
	case v.Etcd == EtcdVoter:
		if !s.EtcdQuorum() {
			return StepNone, ErrNoEtcdQuorum
		}
		if !s.EtcdQuorumWithout(v.Name) {
			return StepNone, ErrQuorumAtRisk
		}
		return StepRemoveVoter, nil

	case v.EtcdRunning:
		return StepStopEtcd, nil

	case v.Etcd == EtcdLearner:
		if !s.EtcdQuorum() {
			return StepNone, ErrNoEtcdQuorum
		}
		return StepRemoveLearner, nil

	case v.Member != MemberNone:
		if !s.DqliteQuorum() {
			return StepNone, ErrNoDqliteQuorum
		}
		if v.Member == MemberJoined && !s.DqliteQuorumWithout(v.Name) {
			return StepNone, ErrQuorumAtRisk
		}
		return StepLeaveCluster, nil

	case !s.DqliteQuorum():
		return StepNone, ErrNoDqliteQuorum

	default:
		return StepFinishTeardown, nil
	}
}
