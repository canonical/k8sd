package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	etcdclient "github.com/canonical/k8sd/pkg/client/etcd"
	. "github.com/onsi/gomega"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
)

// fakeClock advances its time on every wait, so retry budgets are exercised
// without sleeping.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

// revertRecorder records the side effects of RevertEtcdJoin in order.
type revertRecorder struct {
	events []string
	// removeErrs is returned by successive removal calls; the last entry
	// repeats once exhausted. Empty means every removal succeeds.
	removeErrs []error
	removals   int
}

func (r *revertRecorder) remover() EtcdMemberRemover {
	return EtcdMemberRemoverFunc(func(context.Context) error {
		r.events = append(r.events, "remove")
		r.removals++
		if len(r.removeErrs) == 0 {
			return nil
		}
		return r.removeErrs[min(r.removals, len(r.removeErrs))-1]
	})
}

func (r *revertRecorder) revert(mayBeVoter bool, clock Clock) EtcdJoinRevert {
	return EtcdJoinRevert{
		MemberID:      0xabc,
		MayBeVoter:    mayBeVoter,
		EtcdStarted:   true,
		RemoveVoter:   r.remover(),
		RemoveLearner: r.remover(),
		StopEtcd: EtcdStopperFunc(func(context.Context) error {
			r.events = append(r.events, "stop")
			return nil
		}),
		RemoveDir: DirRemoverFunc(func() error {
			r.events = append(r.events, "rmdir")
			return nil
		}),
		Clock: clock,
	}
}

func TestRevertEtcdJoin_VoterRemovalRejected_KeepsEtcd(t *testing.T) {
	g := NewWithT(t)
	clock := &fakeClock{now: time.Unix(0, 0)}
	rec := &revertRecorder{removeErrs: []error{rpctypes.ErrUnhealthy}}

	RevertEtcdJoin(context.Background(), rec.revert(true, clock))

	g.Expect(rec.events).NotTo(ContainElement("stop"), "a voter that is still a member must keep running")
	g.Expect(rec.events).NotTo(ContainElement("rmdir"), "a voter that is still a member must keep its data")
	g.Expect(rec.removals).To(BeNumerically(">", 1), "removal must be retried")
	g.Expect(clock.now.Sub(time.Unix(0, 0))).To(BeNumerically("<=", DefaultEtcdJoinRevertRemoveTimeout), "retries must stay within the revert deadline")
	g.Expect(clock.now.Sub(time.Unix(0, 0))).To(BeNumerically(">", 5*time.Second), "retries must outlast etcd's 5s HealthInterval")
}

func TestRevertEtcdJoin_VoterRemovalRejectedThenAccepted(t *testing.T) {
	g := NewWithT(t)
	rec := &revertRecorder{removeErrs: []error{rpctypes.ErrUnhealthy, rpctypes.ErrUnhealthy, nil}}

	RevertEtcdJoin(context.Background(), rec.revert(true, &fakeClock{}))

	g.Expect(rec.events).To(Equal([]string{"remove", "remove", "remove", "stop", "rmdir"}))
}

func TestRevertEtcdJoin_VoterMemberNotFound_TreatedAsRemoved(t *testing.T) {
	for name, notFound := range map[string]error{
		"etcd":    rpctypes.ErrMemberNotFound,
		"wrapped": fmt.Errorf("remove: %w", etcdclient.ErrNotFound),
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			rec := &revertRecorder{removeErrs: []error{notFound}}

			RevertEtcdJoin(context.Background(), rec.revert(true, &fakeClock{}))

			g.Expect(rec.events).To(Equal([]string{"remove", "stop", "rmdir"}))
		})
	}
}

func TestRevertEtcdJoin_VoterWithoutEndpoints_KeepsEtcd(t *testing.T) {
	g := NewWithT(t)
	rec := &revertRecorder{}
	r := rec.revert(true, &fakeClock{})
	r.RemoveVoter = nil

	RevertEtcdJoin(context.Background(), r)

	g.Expect(rec.events).To(BeEmpty())
}

func TestRevertEtcdJoin_VoterContextCancelled_KeepsEtcd(t *testing.T) {
	g := NewWithT(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &revertRecorder{removeErrs: []error{context.Canceled}}

	// A real clock: the cancelled context must end the retries, not the budget.
	RevertEtcdJoin(ctx, rec.revert(true, nil))

	g.Expect(rec.events).To(Equal([]string{"remove"}))
}

func TestRevertEtcdJoin_Learner(t *testing.T) {
	for _, tc := range []struct {
		name        string
		etcdStarted bool
		noEndpoints bool
		removeErr   error
		expected    []string
	}{
		{name: "StopThenRemoveThenWipe", etcdStarted: true, expected: []string{"stop", "remove", "rmdir"}},
		{name: "RemovalFailsNotRetried", etcdStarted: true, removeErr: rpctypes.ErrUnhealthy, expected: []string{"stop", "remove", "rmdir"}},
		{name: "NotStarted", expected: []string{"remove", "rmdir"}},
		{name: "NoEndpoints", etcdStarted: true, noEndpoints: true, expected: []string{"stop", "rmdir"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			rec := &revertRecorder{}
			if tc.removeErr != nil {
				rec.removeErrs = []error{tc.removeErr}
			}
			r := rec.revert(false, &fakeClock{})
			r.EtcdStarted = tc.etcdStarted
			if tc.noEndpoints {
				r.RemoveLearner = nil
			}

			RevertEtcdJoin(context.Background(), r)

			g.Expect(rec.events).To(Equal(tc.expected))
		})
	}
}

func TestRevertEtcdJoin_StopFailureAfterRemovalStillWipes(t *testing.T) {
	g := NewWithT(t)
	rec := &revertRecorder{}
	r := rec.revert(true, &fakeClock{})
	r.StopEtcd = EtcdStopperFunc(func(context.Context) error { return errors.New("snapctl failed") })

	RevertEtcdJoin(context.Background(), r)

	g.Expect(rec.events).To(Equal([]string{"remove", "rmdir"}), "a removed member's data is safe to delete")
}
