package lifecycle_test

import (
	"errors"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/lifecycle"
	. "github.com/onsi/gomega"
)

type nv = lifecycle.NodeView

func active(n string) nv {
	return nv{Name: n, Phase: lifecycle.PhaseActive, Member: lifecycle.MemberJoined, Etcd: lifecycle.EtcdVoter, EtcdRunning: true, Up: true}
}

func down(v nv) nv { v.Up, v.EtcdRunning = false, false; return v }

func node(n string, phase lifecycle.Phase, member lifecycle.MemberRole, etcd lifecycle.EtcdRole, running bool) nv {
	return nv{Name: n, Phase: phase, Member: member, Etcd: etcd, EtcdRunning: running, Up: true}
}

func snapshot(lease string, views ...nv) lifecycle.Snapshot {
	s := lifecycle.Snapshot{Lease: lease, Nodes: map[string]nv{}}
	for _, v := range views {
		s.Nodes[v.Name] = v
	}
	return s
}

const (
	joining     = lifecycle.PhaseJoining
	rollingBack = lifecycle.PhaseRollingBack
	removing    = lifecycle.PhaseRemoving
	absent      = lifecycle.PhaseAbsent
	mNone       = lifecycle.MemberNone
	mPending    = lifecycle.MemberPending
	mJoined     = lifecycle.MemberJoined
	eNone       = lifecycle.EtcdNone
	eLearner    = lifecycle.EtcdLearner
	eVoter      = lifecycle.EtcdVoter
)

// TestPlanVectors pins Plan to the guards of NodeLifecycle.tla. Each vector
// is a reachable TLC state; the comment names the spec action or the
// counterexample it encodes.
func TestPlanVectors(t *testing.T) {
	orphanLearner := lifecycle.EtcdMember{ID: 99, PeerURLs: []string{peerURL("ghost")}, IsLearner: true}
	withOrphan := func(s lifecycle.Snapshot) lifecycle.Snapshot {
		s.Orphans = []lifecycle.EtcdMember{orphanLearner}
		return s
	}

	for _, tc := range []struct {
		name string
		snap lifecycle.Snapshot
		node string
		step lifecycle.Step
		err  error
	}{
		{"stable Absent", snapshot("", active("n1")), "n2", lifecycle.StepNone, nil},
		{"stable Active", snapshot("", active("n1")), "n1", lifecycle.StepNone, nil},

		// Join path.
		{"ConfirmMember is microcluster's", snapshot("n2", active("n1"), node("n2", joining, mPending, eNone, false)), "n2", lifecycle.StepNone, lifecycle.ErrAwaitingMember},
		{"AddLearner", snapshot("n2", active("n1"), node("n2", joining, mJoined, eNone, false)), "n2", lifecycle.StepAddLearner, nil},
		{"AddLearner blocked by orphan learner (lost MemberAdd response, old reverter)", withOrphan(snapshot("n2", active("n1"), node("n2", joining, mJoined, eNone, false))), "n2", lifecycle.StepNone, lifecycle.ErrLearnerBusy},
		{"AddLearner blocked without etcd quorum", snapshot("n2", active("n1"), down(active("n3")), down(active("n4")), node("n2", joining, mJoined, eNone, false)), "n2", lifecycle.StepNone, lifecycle.ErrNoEtcdQuorum},
		{"StartEtcd", snapshot("n2", active("n1"), node("n2", joining, mJoined, eLearner, false)), "n2", lifecycle.StepStartEtcd, nil},
		{"StartEtcd blocked while joiner is down", snapshot("n2", active("n1"), down(node("n2", joining, mJoined, eLearner, false))), "n2", lifecycle.StepNone, lifecycle.ErrNodeDown},
		{"Promote", snapshot("n2", active("n1"), node("n2", joining, mJoined, eLearner, true)), "n2", lifecycle.StepPromote, nil},
		{"Activate", snapshot("n2", active("n1"), node("n2", joining, mJoined, eVoter, true)), "n2", lifecycle.StepActivate, nil},
		{"rebooted voter restarts etcd before Activate", snapshot("n2", active("n1"), node("n2", joining, mJoined, eVoter, false)), "n2", lifecycle.StepStartEtcd, nil},
		{"Activate blocked when seed is down (2 voters, 1 live)", snapshot("n2", down(active("n1")), node("n2", joining, mJoined, eVoter, true)), "n2", lifecycle.StepNone, lifecycle.ErrNoEtcdQuorum},

		// Teardown path.
		{"stop-before-remove counterexample: promoted joiner is removed first", snapshot("n2", active("n1"), node("n2", rollingBack, mJoined, eVoter, true)), "n2", lifecycle.StepRemoveVoter, nil},
		{"unguarded-remove counterexample: removal refused when it would lose quorum", snapshot("n3", down(active("n1")), active("n2"), node("n3", rollingBack, mJoined, eVoter, true)), "n3", lifecycle.StepNone, lifecycle.ErrQuorumAtRisk},
		{"removing a down voter keeps quorum among the others", snapshot("n3", active("n1"), active("n2"), down(node("n3", removing, mJoined, eVoter, false))), "n3", lifecycle.StepRemoveVoter, nil},
		{"dead peer in a 2-voter cluster cannot be removed", snapshot("n2", active("n1"), down(node("n2", removing, mJoined, eVoter, false))), "n2", lifecycle.StepNone, lifecycle.ErrNoEtcdQuorum},
		{"learner is stopped before removal", snapshot("n2", active("n1"), node("n2", rollingBack, mJoined, eLearner, true)), "n2", lifecycle.StepStopEtcd, nil},
		{"RemoveLearner", snapshot("n2", active("n1"), node("n2", rollingBack, mJoined, eLearner, false)), "n2", lifecycle.StepRemoveLearner, nil},
		{"StopEtcd after voter removal", snapshot("n2", active("n1"), node("n2", removing, mJoined, eNone, true)), "n2", lifecycle.StepStopEtcd, nil},
		{"LeaveCluster", snapshot("n2", active("n1"), node("n2", removing, mJoined, eNone, false)), "n2", lifecycle.StepLeaveCluster, nil},
		{"LeaveCluster for pending member", snapshot("n2", active("n1"), node("n2", rollingBack, mPending, eNone, false)), "n2", lifecycle.StepLeaveCluster, nil},
		{"LeaveCluster refused when dqlite would lose quorum", snapshot("n3", active("n1"), down(active("n2")), node("n3", removing, mJoined, eNone, false)), "n3", lifecycle.StepNone, lifecycle.ErrQuorumAtRisk},
		{"FinishTeardown", snapshot("n2", active("n1"), node("n2", rollingBack, mNone, eNone, false)), "n2", lifecycle.StepFinishTeardown, nil},

		{"transition without the lease is rejected", snapshot("n3", active("n1"), node("n2", joining, mJoined, eNone, false)), "n2", lifecycle.StepNone, lifecycle.ErrLeaseLost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			step, err := lifecycle.Plan(tc.snap, tc.node)
			g.Expect(step).To(Equal(tc.step))
			if tc.err == nil {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(tc.err))
			}
		})
	}
}

func TestBlockedErrorsAreRetryable(t *testing.T) {
	g := NewWithT(t)
	for _, err := range []error{lifecycle.ErrNoEtcdQuorum, lifecycle.ErrNoDqliteQuorum, lifecycle.ErrQuorumAtRisk, lifecycle.ErrNodeDown, lifecycle.ErrLearnerBusy, lifecycle.ErrAwaitingMember} {
		g.Expect(errors.Is(err, lifecycle.ErrBlocked)).To(BeTrue(), err.Error())
		g.Expect(lifecycle.IsPermanent(err)).To(BeFalse())
	}
	g.Expect(lifecycle.IsPermanent(lifecycle.Permanent(errInjected))).To(BeTrue())
	g.Expect(lifecycle.Permanent(nil)).To(Succeed())
}

func TestCheckInvariantsDetectsViolations(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap lifecycle.Snapshot
		want string
	}{
		{"two transitions", snapshot("n2", active("n1"), node("n2", joining, mJoined, eNone, false), node("n3", removing, mJoined, eVoter, true)), "SingleTransition"},
		{"active learner", snapshot("", active("n1"), node("n2", lifecycle.PhaseActive, mJoined, eLearner, true)), "ActiveIsConfirmedVoter"},
		{"orphan etcd member", snapshot("", active("n1"), node("n2", absent, mNone, eLearner, false)), "AbsentIsClean"},
		{"no voters", snapshot("", node("n1", absent, mNone, eNone, false)), "ControlPlaneNeverEmpty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(tc.snap.CheckInvariants()).To(MatchError(ContainSubstring(tc.want)))
		})
	}
	NewWithT(t).Expect(snapshot("", active("n1"), active("n2")).CheckInvariants()).To(Succeed())
}
