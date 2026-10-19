package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/lifecycle"
	. "github.com/onsi/gomega"
)

const (
	traceSeeds      = 1000
	traceLength     = 80
	traceMaxCrashes = 2 // MaxCrashes in NodeLifecycle.cfg
)

// TestRandomTracesPreserveSpec is a seeded random walk over the action set of
// NodeLifecycle.tla (orchestrator steps, operator requests, crashes, restarts
// and lost responses) against the real Orchestrator. After every mutation the
// simulator checks every invariant and action property of the spec; at the
// end all hosts restart and Resume must converge (TransitionsTerminate).
// A failing seed is a replayable counterexample.
func TestRandomTracesPreserveSpec(t *testing.T) {
	nodes := []string{"n1", "n2", "n3"}
	for seed := uint64(1); seed <= traceSeeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			g := NewWithT(t)
			rng := rand.New(rand.NewPCG(seed, 0x6b387364))
			s := newSim(nodes...)
			o := s.orchestrator()
			ctx := testCtx(t)
			crashes := 0
			var trace []string

			for i := 0; i < traceLength; i++ {
				n := nodes[rng.IntN(len(nodes))]
				var action string
				switch r := rng.IntN(100); {
				case r < 45:
					step, err := o.Step(ctx, n)
					action = fmt.Sprintf("Step(%s)=%s err=%v", n, step, err)
					g.Expect(errors.Is(err, lifecycle.ErrLeaseLost)).To(BeFalse(), action)
				case r < 55:
					action = fmt.Sprintf("BeginJoin(%s) err=%v", n, o.BeginJoin(ctx, n, peerURL(n)))
				case r < 61:
					t := s.truth()
					v := t.Node(n)
					if v.Phase == lifecycle.PhaseAbsent && v.Member == lifecycle.MemberNone && v.Etcd == lifecycle.EtcdNone && v.Up && t.DqliteQuorum() {
						s.admit(n)
						action = "McJoin(" + n + ") err=<nil>"
					}
				case r < 63:
					action = fmt.Sprintf("McRollback(%s) err=%v", n, s.rollbackMember(n))
				case r < 66:
					action = fmt.Sprintf("Abort(%s) err=%v", n, o.Abort(ctx, n))
				case r < 72:
					action = fmt.Sprintf("BeginRemove(%s) err=%v", n, o.BeginRemove(ctx, n))
				case r < 80:
					if crashes < traceMaxCrashes && s.truth().Node(n).Up {
						crashes++
						s.crash(n)
						action = "Crash(" + n + ")"
					}
				case r < 92:
					s.restart(n)
					action = "Restart(" + n + ")"
				default:
					s.inject("*", fault{commit: rng.IntN(2) == 0, err: errInjected})
					action = "InjectFaultOnNextOp"
				}
				trace = append(trace, action)
				if v := s.Violations(); len(v) > 0 {
					t.Fatalf("spec violated: %v\ntrace:\n%v", v, trace)
				}
			}

			// Liveness: once every host is back, the in-flight transition
			// terminates (TransitionsTerminate) and microcluster rolls back
			// members that never took the lease (AdmissionsResolve).
			s.mu.Lock()
			s.faults = map[string][]fault{}
			s.mu.Unlock()
			for _, n := range nodes {
				if !s.truth().Node(n).Up {
					s.restart(n)
				}
			}
			resumeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			lease, phase, err := o.Resume(resumeCtx)
			g.Expect(err).NotTo(HaveOccurred(), "trace: %v", trace)
			if lease != "" {
				g.Expect(phase).To(BeElementOf(lifecycle.PhaseActive, lifecycle.PhaseAbsent))
			}
			g.Expect(s.truth().Lease).To(BeEmpty())
			for _, n := range nodes {
				if v := s.truth().Node(n); v.Phase == lifecycle.PhaseAbsent && v.Member != lifecycle.MemberNone {
					g.Expect(s.rollbackMember(n)).To(Succeed(), "trace: %v", trace)
				}
			}
			g.Expect(s.Violations()).To(BeEmpty(), "trace: %v", trace)
			g.Expect(s.truth().CheckInvariants()).To(Succeed(), "trace: %v", trace)
		})
	}
}
