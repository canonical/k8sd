package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CoreDNSConfigMapController watches the k8sd-coredns-values ConfigMap and triggers
// DNS reconciliation whenever the ConfigMap is created, updated, or deleted.
type CoreDNSConfigMapController struct {
	snap      snap.Snap
	notifyDNS func()
}

func NewCoreDNSConfigMapController(snap snap.Snap, notifyDNS func()) *CoreDNSConfigMapController {
	return &CoreDNSConfigMapController{
		snap:      snap,
		notifyDNS: notifyDNS,
	}
}

func (c *CoreDNSConfigMapController) Run(ctx context.Context) {
	ctx = log.NewContext(ctx, log.FromContext(ctx).WithValues("controller", "coredns-configmap"))
	log := log.FromContext(ctx)

	for {
		if err := c.watch(ctx); err != nil {
			log.Error(err, "ConfigMap watcher error, retrying in 30s")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func (c *CoreDNSConfigMapController) watch(ctx context.Context) error {
	log := log.FromContext(ctx)

	client, err := getNewK8sClientWithRetries(ctx, c.snap, false)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	watcher, err := client.CoreV1().ConfigMaps("kube-system").Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=k8sd-coredns-values",
	})
	if err != nil {
		return fmt.Errorf("failed to watch ConfigMap: %w", err)
	}
	defer watcher.Stop()

	log.Info("Started watching k8sd-coredns-values ConfigMap")

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watcher channel closed")
			}
			log.Info("ConfigMap event received", "type", event.Type)
			c.notifyDNS()
		}
	}
}
