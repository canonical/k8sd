package controllers_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/controllers"
	"github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

func TestServiceRestartController(t *testing.T) {
	t.Run("RestartsPendingServices", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s := &mock.Snap{
			ServicesToRestartResult: []string{"kubelet", "containerd"},
		}

		triggerCh := make(chan time.Time)
		ctrl := controllers.NewServiceRestartController(s, triggerCh)
		go ctrl.Run(ctx)

		triggerCh <- time.Now()

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("timed out waiting for reconciliation to complete")
		}

		g.Expect(s.RestartServicesCalledWith).To(HaveLen(1))
		g.Expect(s.RestartServicesCalledWith[0]).To(ConsistOf("kubelet", "containerd"))
	})

	t.Run("NoRestartWhenNoPendingServices", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s := &mock.Snap{
			ServicesToRestartResult: nil,
		}

		triggerCh := make(chan time.Time)
		ctrl := controllers.NewServiceRestartController(s, triggerCh)
		go ctrl.Run(ctx)

		triggerCh <- time.Now()

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("timed out waiting for reconciliation to complete")
		}

		g.Expect(s.RestartServicesCalledWith).To(BeEmpty())
	})

	t.Run("ContinuesAfterServicesToRestartError", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s := &mock.Snap{
			ServicesToRestartErr: fmt.Errorf("disk read error"),
		}

		triggerCh := make(chan time.Time)
		ctrl := controllers.NewServiceRestartController(s, triggerCh)
		go ctrl.Run(ctx)

		triggerCh <- time.Now()

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("timed out waiting for reconciliation to complete")
		}

		g.Expect(s.RestartServicesCalledWith).To(BeEmpty())

		// second tick still runs
		s.ServicesToRestartErr = nil
		s.ServicesToRestartResult = []string{"kubelet"}

		triggerCh <- time.Now()

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("timed out waiting for second reconciliation to complete")
		}

		g.Expect(s.RestartServicesCalledWith).To(HaveLen(1))
	})

	t.Run("ContinuesAfterRestartServicesError", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s := &mock.Snap{
			ServicesToRestartResult: []string{"kubelet"},
			RestartServicesErr:      fmt.Errorf("snap restart failed"),
		}

		triggerCh := make(chan time.Time)
		ctrl := controllers.NewServiceRestartController(s, triggerCh)
		go ctrl.Run(ctx)

		triggerCh <- time.Now()

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("timed out waiting for reconciliation to complete")
		}

		// second tick still runs
		s.RestartServicesErr = nil

		triggerCh <- time.Now()

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("timed out waiting for second reconciliation to complete")
		}

		g.Expect(s.RestartServicesCalledWith).To(HaveLen(2))
	})

	t.Run("StopsOnContextCancellation", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())

		s := &mock.Snap{}
		triggerCh := make(chan time.Time)
		ctrl := controllers.NewServiceRestartController(s, triggerCh)

		done := make(chan struct{})
		go func() {
			ctrl.Run(ctx)
			close(done)
		}()

		cancel()

		select {
		case <-done:
		case <-time.After(channelSendTimeout):
			g.Fail("controller did not stop after context cancellation")
		}
	})
}
