package lifecycle_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/lifecycle"
	. "github.com/onsi/gomega"
)

func expectHealthy(g Gomega, s *sim) {
	g.Expect(s.Violations()).To(BeEmpty())
	g.Expect(s.truth().CheckInvariants()).To(Succeed())
}

func expectPhase(g Gomega, s *sim, n string, p lifecycle.Phase) {
	g.Expect(s.truth().Node(n).Phase).To(Equal(p))
}

func indexOf(ops []string, op string) int { return slices.Index(ops, op) }

func TestJoinHappyPath(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2", "n3")
	o := s.orchestrator()

	s.admit("n2")
	g.Expect(o.Join(ctx, "n2", peerURL("n2"))).To(Succeed())
	s.admit("n3")
	g.Expect(o.Join(ctx, "n3", peerURL("n3"))).To(Succeed())

	g.Expect(s.opsLog()).To(Equal([]string{
		"journal.CAS(n2:Absent->Joining)",
		"etcd.AddLearner(n2)", "hosts.StartEtcd(n2)", "etcd.Promote(n2)",
		"journal.CAS(n2:Joining->Active)",
		"journal.CAS(n3:Absent->Joining)",
		"etcd.AddLearner(n3)", "hosts.StartEtcd(n3)", "etcd.Promote(n3)",
		"journal.CAS(n3:Joining->Active)",
	}))
	for _, n := range []string{"n1", "n2", "n3"} {
		expectPhase(g, s, n, lifecycle.PhaseActive)
	}
	g.Expect(s.truth().Lease).To(BeEmpty())
	expectHealthy(g, s)
}

// The existing hook learns the learner ID only from the MemberAddAsLearner
// response; if the response is lost after commit, the learner is orphaned
// and blocks every later join (etcd allows one learner). Observation-based
// recovery finds the member by peer URL instead.
func TestJoinRecoversFromLostResponses(t *testing.T) {
	for _, op := range []string{
		"etcd.AddLearner(n2)",
		"hosts.StartEtcd(n2)",
		"etcd.Promote(n2)",
		"journal.CAS(n2:Joining->Active)",
	} {
		t.Run(op, func(t *testing.T) {
			g := NewWithT(t)
			s := newSim("n1", "n2")
			s.inject(op, fault{commit: true, err: errInjected})

			s.admit("n2")
			g.Expect(s.orchestrator().Join(testCtx(t), "n2", peerURL("n2"))).To(Succeed())
			expectPhase(g, s, "n2", lifecycle.PhaseActive)
			g.Expect(s.etcdMembersFor("n2")).To(Equal(1))
			g.Expect(s.opsLog()).To(HaveEach(Not(ContainSubstring("Remove"))))
			expectHealthy(g, s)
		})
	}
}

func TestJoinRetriesTransientFailures(t *testing.T) {
	g := NewWithT(t)
	s := newSim("n1", "n2")
	for _, op := range []string{"etcd.AddLearner(n2)", "etcd.Promote(n2)", "etcd.Promote(n2)", "hosts.StartEtcd(n2)"} {
		s.inject(op, fault{err: errInjected})
	}
	s.admit("n2")
	g.Expect(s.orchestrator().Join(testCtx(t), "n2", peerURL("n2"))).To(Succeed())
	expectPhase(g, s, "n2", lifecycle.PhaseActive)
	expectHealthy(g, s)
}

func TestJoinerRebootsAfterPromote(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2")
	o := s.orchestrator()

	s.admit("n2")
	g.Expect(o.BeginJoin(ctx, "n2", peerURL("n2"))).To(Succeed())
	s.inject("etcd.Promote(n2)", fault{commit: true, then: func(s *sim) { s.crashLocked("n2") }})
	stepUntil(t, ctx, o, "n2", lifecycle.StepPromote)

	// 2 voters, 1 live: no quorum, nothing may proceed, nothing is torn down.
	_, err := o.Step(ctx, "n2")
	g.Expect(err).To(HaveOccurred())
	expectPhase(g, s, "n2", lifecycle.PhaseJoining)

	s.restart("n2")
	phase, err := o.Reconcile(ctx, "n2")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(phase).To(Equal(lifecycle.PhaseActive))
	expectHealthy(g, s)
}

func TestJoinerRebootsBeforeStartingEtcd(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2", "n3")
	s.seedActive("n2")
	o := s.orchestrator()

	s.admit("n3")
	g.Expect(o.BeginJoin(ctx, "n3", peerURL("n3"))).To(Succeed())
	s.inject("etcd.AddLearner(n3)", fault{commit: true, err: errInjected, then: func(s *sim) { s.crashLocked("n3") }})
	_, err := o.Step(ctx, "n3")
	g.Expect(err).To(MatchError(errInjected))

	_, err = o.Step(ctx, "n3")
	g.Expect(err).To(MatchError(lifecycle.ErrNodeDown))

	s.restart("n3")
	phase, err := o.Reconcile(ctx, "n3")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(phase).To(Equal(lifecycle.PhaseActive))
	expectHealthy(g, s)
}

// TLC counterexample for the "stop-before-remove" mutation: stopping etcd on
// a promoted joiner in a 2-voter cluster loses quorum and the voter can then
// never be removed. The orchestrator removes the voter first.
func TestFailedJoinRemovesVoterBeforeStoppingEtcd(t *testing.T) {
	g := NewWithT(t)
	s := newSim("n1", "n2")
	o := s.orchestrator(lifecycle.WithActivationCheck(func(context.Context, string) error {
		return lifecycle.Permanent(errors.New("kube-apiserver never became ready"))
	}))

	s.admit("n2")
	err := o.Join(testCtx(t), "n2", peerURL("n2"))
	g.Expect(err).To(MatchError(ContainSubstring("kube-apiserver never became ready")))

	ops := s.opsLog()
	g.Expect(ops).To(ContainElements("journal.CAS(n2:Joining->RollingBack)", "etcd.Remove(n2)", "hosts.StopEtcd(n2)", "members.Remove(n2)", "journal.CAS(n2:RollingBack->Absent)"))
	g.Expect(indexOf(ops, "etcd.Remove(n2)")).To(BeNumerically("<", indexOf(ops, "hosts.StopEtcd(n2)")))
	expectPhase(g, s, "n2", lifecycle.PhaseAbsent)
	g.Expect(s.truth().Lease).To(BeEmpty())
	expectHealthy(g, s)
}

func TestFailedJoinAsLearnerStopsThenRemoves(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2")
	o := s.orchestrator()

	s.admit("n2")
	g.Expect(o.BeginJoin(ctx, "n2", peerURL("n2"))).To(Succeed())
	stepUntil(t, ctx, o, "n2", lifecycle.StepStartEtcd)
	g.Expect(o.Abort(ctx, "n2")).To(Succeed())
	g.Expect(o.Abort(ctx, "n2")).To(Succeed(), "Abort is idempotent")

	phase, err := o.Reconcile(ctx, "n2")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(phase).To(Equal(lifecycle.PhaseAbsent))
	ops := s.opsLog()
	g.Expect(indexOf(ops, "hosts.StopEtcd(n2)")).To(BeNumerically("<", indexOf(ops, "etcd.Remove(n2)")))
	expectHealthy(g, s)
}

// TLC counterexample for the "unguarded-remove" mutation: with a peer down,
// rolling back a promoted joiner in a 3-voter cluster would leave 1 of 2
// voters live. The current hook reverter calls MemberRemove, ignores etcd's
// rejection, and stops etcd anyway. The orchestrator waits instead.
func TestRollbackNeverBreaksQuorum(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2", "n3")
	s.seedActive("n2")
	o := s.orchestrator()

	s.admit("n3")
	g.Expect(o.BeginJoin(ctx, "n3", peerURL("n3"))).To(Succeed())
	stepUntil(t, ctx, o, "n3", lifecycle.StepPromote)
	s.crash("n1")
	g.Expect(o.Abort(ctx, "n3")).To(Succeed())

	for i := 0; i < 3; i++ {
		step, err := o.Step(ctx, "n3")
		g.Expect(step).To(Equal(lifecycle.StepNone))
		g.Expect(err).To(MatchError(lifecycle.ErrQuorumAtRisk))
	}
	g.Expect(s.opsLog()).NotTo(ContainElement("hosts.StopEtcd(n3)"))
	g.Expect(s.truth().EtcdQuorum()).To(BeTrue())

	shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := o.Reconcile(shortCtx, "n3")
	g.Expect(err).To(MatchError(context.DeadlineExceeded))
	g.Expect(err).To(MatchError(lifecycle.ErrQuorumAtRisk))

	s.restart("n1")
	lease, phase, err := o.Resume(ctx)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(lease).To(Equal("n3"))
	g.Expect(phase).To(Equal(lifecycle.PhaseAbsent))
	expectHealthy(g, s)
}

func TestJoinTimeoutRollsBackWithDetachedContext(t *testing.T) {
	g := NewWithT(t)
	s := newSim("n1", "n2")
	activationCalls := 0
	o := s.orchestrator(lifecycle.WithActivationCheck(func(ctx context.Context, _ string) error {
		activationCalls++
		return errors.New("apiserver not ready yet")
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	s.admit("n2")
	err := o.Join(ctx, "n2", peerURL("n2"))
	g.Expect(err).To(MatchError(context.DeadlineExceeded))
	g.Expect(activationCalls).To(BeNumerically(">", 1))
	expectPhase(g, s, "n2", lifecycle.PhaseAbsent)
	g.Expect(s.etcdMembersFor("n2")).To(BeZero())
	expectHealthy(g, s)
}

func TestRemove(t *testing.T) {
	t.Run("RefusesLastControlPlane", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1")
		g.Expect(s.orchestrator().Remove(testCtx(t), "n1")).To(MatchError(lifecycle.ErrLastControlPlane))
		g.Expect(s.opsLog()).To(BeEmpty())
	})

	t.Run("RefusesNonActive", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1", "n2")
		g.Expect(s.orchestrator().Remove(testCtx(t), "n2")).To(MatchError(lifecycle.ErrInvalidPhase))
	})

	t.Run("HealthyNode", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1", "n2", "n3")
		s.seedActive("n2")
		s.seedActive("n3")
		g.Expect(s.orchestrator().Remove(testCtx(t), "n2")).To(Succeed())
		g.Expect(s.opsLog()).To(Equal([]string{
			"journal.CAS(n2:Active->Removing)",
			"etcd.Remove(n2)", "hosts.StopEtcd(n2)", "members.Remove(n2)",
			"journal.CAS(n2:Removing->Absent)",
		}))
		expectHealthy(g, s)
	})

	t.Run("DownNode", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1", "n2", "n3")
		s.seedActive("n2")
		s.seedActive("n3")
		s.crash("n3")
		g.Expect(s.orchestrator().Remove(testCtx(t), "n3")).To(Succeed())
		g.Expect(s.opsLog()).NotTo(ContainElement("hosts.StopEtcd(n3)"))
		expectPhase(g, s, "n3", lifecycle.PhaseAbsent)
		expectHealthy(g, s)
	})

	// #161, the dqlite spare dies: a 2-node cluster has 1 dqlite voter and
	// 1 spare but 2 etcd voters. dqlite still has quorum, etcd does not. The
	// node must stay a microcluster member and an etcd voter until it comes
	// back; remove-node --force used to drop it from microcluster here and
	// orphan the etcd voter.
	t.Run("DeadDqliteSpareOfTwoNodeClusterStaysMember", func(t *testing.T) {
		g := NewWithT(t)
		ctx := testCtx(t)
		s := newSim("n1", "n2")
		s.seedActive("n2")
		o := s.orchestrator()
		g.Expect(s.truth().Node("n2").Dqlite).To(Equal(lifecycle.DqliteSpare))
		expectStillMember := func() {
			v := s.truth().Node("n2")
			g.Expect(v.Member).To(Equal(lifecycle.MemberJoined))
			g.Expect(v.Etcd).To(Equal(lifecycle.EtcdVoter))
		}

		// Observing etcd needs quorum (linearizable MemberList), so the
		// removal cannot even begin.
		s.crash("n2")
		g.Expect(o.Remove(ctx, "n2")).To(MatchError(errEtcdNoLeader))
		g.Expect(s.opsLog()).To(BeEmpty())
		expectStillMember()

		// The peer dies right after the removal began: the transition keeps
		// the lease and waits instead of dropping the member.
		s.restart("n2")
		s.inject("journal.CAS(n2:Active->Removing)", fault{commit: true, then: func(s *sim) { s.crashLocked("n2") }})
		shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		g.Expect(o.Remove(shortCtx, "n2")).To(MatchError(ContainSubstring("resume required")))
		expectPhase(g, s, "n2", lifecycle.PhaseRemoving)
		g.Expect(s.truth().Lease).To(Equal("n2"))
		g.Expect(s.opsLog()).NotTo(ContainElement("members.Remove(n2)"))
		expectStillMember()

		s.restart("n2")
		_, phase, err := o.Resume(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(phase).To(Equal(lifecycle.PhaseAbsent))
		expectHealthy(g, s)
	})

	// #161, the dqlite voter dies: nothing can begin without dqlite quorum.
	t.Run("DeadDqliteVoterOfTwoNodeClusterBlocksRemoval", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1", "n2")
		s.seedActive("n2")
		o := s.orchestrator()
		g.Expect(s.truth().Node("n1").Dqlite).To(Equal(lifecycle.DqliteVoter))

		s.crash("n1")
		g.Expect(o.Remove(testCtx(t), "n1")).To(MatchError(errDqliteNoLeader))
		g.Expect(s.opsLog()).To(BeEmpty())
		s.restart("n1")
		expectHealthy(g, s)
	})

	t.Run("RebootMidRemovalRevivesEtcd", func(t *testing.T) {
		g := NewWithT(t)
		ctx := testCtx(t)
		s := newSim("n1", "n2", "n3")
		s.seedActive("n2")
		o := s.orchestrator()

		s.admit("n3")
		g.Expect(o.BeginJoin(ctx, "n3", peerURL("n3"))).To(Succeed())
		stepUntil(t, ctx, o, "n3", lifecycle.StepStartEtcd)
		g.Expect(o.Abort(ctx, "n3")).To(Succeed())
		stepUntil(t, ctx, o, "n3", lifecycle.StepStopEtcd)

		// Host reboots before the learner is removed; the snap service
		// brings etcd back because the host is still an etcd member.
		s.crash("n3")
		s.restart("n3")
		g.Expect(s.truth().Node("n3").EtcdRunning).To(BeTrue())

		phase, err := o.Reconcile(ctx, "n3")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(phase).To(Equal(lifecycle.PhaseAbsent))
		stops := 0
		for _, op := range s.opsLog() {
			if op == "hosts.StopEtcd(n3)" {
				stops++
			}
		}
		g.Expect(stops).To(Equal(2))
		expectHealthy(g, s)
	})
}

func TestDqlitePartitionBlocksAllTransitions(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2", "n3", "n4")
	s.seedActive("n2")
	s.seedActive("n3")
	o := s.orchestrator()
	s.admit("n4")
	s.crash("n2")
	s.crash("n3")

	g.Expect(o.BeginJoin(ctx, "n4", peerURL("n4"))).To(MatchError(errDqliteNoLeader))
	g.Expect(o.BeginRemove(ctx, "n2")).To(MatchError(errDqliteNoLeader))
	g.Expect(s.opsLog()).To(BeEmpty())

	s.restart("n2")
	g.Expect(o.Join(ctx, "n4", peerURL("n4"))).To(Succeed())
	expectHealthy(g, s)
}

// An orchestrator process dying mid-join must not leak state: a fresh
// instance resumes from the journal and observed state alone.
func TestResumeAfterOrchestratorCrash(t *testing.T) {
	for _, after := range []lifecycle.Step{lifecycle.StepAddLearner, lifecycle.StepStartEtcd, lifecycle.StepPromote} {
		t.Run(string(after), func(t *testing.T) {
			g := NewWithT(t)
			ctx := testCtx(t)
			s := newSim("n1", "n2")
			o := s.orchestrator()
			s.admit("n2")
			g.Expect(o.BeginJoin(ctx, "n2", peerURL("n2"))).To(Succeed())
			stepUntil(t, ctx, o, "n2", after)

			lease, phase, err := s.orchestrator().Resume(ctx)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(lease).To(Equal("n2"))
			g.Expect(phase).To(Equal(lifecycle.PhaseActive))
			expectHealthy(g, s)
		})
	}

	t.Run("NothingInFlight", func(t *testing.T) {
		g := NewWithT(t)
		lease, _, err := newSim("n1").orchestrator().Resume(testCtx(t))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(lease).To(BeEmpty())
	})
}

func TestBeginJoin(t *testing.T) {
	t.Run("Idempotent", func(t *testing.T) {
		g := NewWithT(t)
		ctx := testCtx(t)
		s := newSim("n1", "n2")
		o := s.orchestrator()
		s.admit("n2")
		g.Expect(o.BeginJoin(ctx, "n2", peerURL("n2"))).To(Succeed())
		g.Expect(o.BeginJoin(ctx, "n2", peerURL("n2"))).To(Succeed())
		g.Expect(s.opsLog()).To(HaveLen(1))
	})

	// An unnamed learner left behind by a join whose MemberAdd response was
	// lost (the failure mode of the current hook reverter).
	addOrphanLearner := func(s *sim, node string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.nextID++
		s.members[s.nextID] = &simMember{id: s.nextID, node: node, learner: true}
	}

	t.Run("RejectsOrphanLearnerWithOwnPeerURL", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1", "n2")
		addOrphanLearner(s, "n2")
		s.admit("n2")
		g.Expect(s.orchestrator().BeginJoin(testCtx(t), "n2", peerURL("n2"))).To(MatchError(lifecycle.ErrInvalidPhase))
		g.Expect(s.opsLog()).To(BeEmpty())
	})

	t.Run("RejectsWhileForeignOrphanLearnerExists", func(t *testing.T) {
		g := NewWithT(t)
		s := newSim("n1", "n2", "ghost")
		addOrphanLearner(s, "ghost")
		s.admit("n2")
		g.Expect(s.orchestrator().BeginJoin(testCtx(t), "n2", peerURL("n2"))).To(MatchError(lifecycle.ErrLearnerBusy))
		g.Expect(s.opsLog()).To(BeEmpty())
	})

	t.Run("RequiresPeerURL", func(t *testing.T) {
		g := NewWithT(t)
		err := newSim("n1").orchestrator().BeginJoin(testCtx(t), "n2", "")
		g.Expect(lifecycle.IsPermanent(err)).To(BeTrue())
	})
}

func TestConcurrentJoinsAreSerialisedByLease(t *testing.T) {
	for i := 0; i < 25; i++ {
		g := NewWithT(t)
		ctx := testCtx(t)
		s := newSim("n1", "n2", "n3", "n4")
		o := s.orchestrator()
		for _, n := range []string{"n2", "n3", "n4"} {
			s.admit(n)
		}

		var wg sync.WaitGroup
		errs := make([]error, 3)
		for j, n := range []string{"n2", "n3", "n4"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[j] = o.Join(ctx, n, peerURL(n))
			}()
		}
		wg.Wait()

		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++
				continue
			}
			g.Expect(err).To(MatchError(lifecycle.ErrLeaseHeld))
		}
		g.Expect(succeeded).To(BeNumerically(">=", 1))
		expectHealthy(g, s)
	}
}

// Independent orchestrator instances share nothing but the journal and the
// external systems, like k8sd daemons on different hosts.
func TestConcurrentReconcilersConverge(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	s := newSim("n1", "n2")
	s.admit("n2")
	g.Expect(s.orchestrator().BeginJoin(ctx, "n2", peerURL("n2"))).To(Succeed())

	var wg sync.WaitGroup
	phases := make([]lifecycle.Phase, 8)
	errs := make([]error, 8)
	for i := range phases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			phases[i], errs[i] = s.orchestrator().Reconcile(ctx, "n2")
		}()
	}
	wg.Wait()

	for i := range phases {
		g.Expect(errs[i]).NotTo(HaveOccurred())
		g.Expect(phases[i]).To(Equal(lifecycle.PhaseActive))
	}
	g.Expect(s.etcdMembersFor("n2")).To(Equal(1))
	expectHealthy(g, s)
}
