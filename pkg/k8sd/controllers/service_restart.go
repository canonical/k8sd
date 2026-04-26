package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
)

// ServiceRestartController periodically checks if any snap services have been
// marked for restart and restarts them.
type ServiceRestartController struct {
	snap snap.Snap
	// triggerCh is used to drive the reconcile loop. Typically a time.Ticker.C.
	triggerCh <-chan time.Time
	// reconciledCh notifies that a reconciliation loop has completed.
	reconciledCh chan struct{}
}

// NewServiceRestartController creates a new ServiceRestartController.
// triggerCh is typically time.NewTicker(<interval>).C.
func NewServiceRestartController(snap snap.Snap, triggerCh <-chan time.Time) *ServiceRestartController {
	return &ServiceRestartController{
		snap:         snap,
		triggerCh:    triggerCh,
		reconciledCh: make(chan struct{}, 1),
	}
}

// Run starts the controller and blocks until ctx is cancelled.
func (c *ServiceRestartController) Run(ctx context.Context) {
	ctx = log.NewContext(ctx, log.FromContext(ctx).WithValues("controller", "service-restart"))
	log := log.FromContext(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.triggerCh:
		}

		log.Info("checking if services need to be restarted")
		if err := c.reconcile(ctx); err != nil {
			log.Error(err, "failed to reconcile service restarts")
		}

		select {
		case c.reconciledCh <- struct{}{}:
		default:
		}
	}
}

func (c *ServiceRestartController) reconcile(ctx context.Context) error {
	log := log.FromContext(ctx)

	needRestart, err := c.snap.ServicesToRestart()
	if err != nil {
		return fmt.Errorf("failed to get services to restart: %w", err)
	}

	if len(needRestart) == 0 {
		log.Info("no services need to be restarted")
		return nil
	}

	log.Info("restarting services", "services", needRestart)

	if err := c.snap.RestartServices(ctx, needRestart); err != nil {
		return fmt.Errorf("failed to restart services: %w", err)
	}

	log.Info("restarted services", "services", needRestart)

	return nil
}

// ReconciledCh returns the channel that receives a value after each reconciliation loop.
func (c *ServiceRestartController) ReconciledCh() <-chan struct{} {
	return c.reconciledCh
}
