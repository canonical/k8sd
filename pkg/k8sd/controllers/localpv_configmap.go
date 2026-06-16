package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// LocalPVConfigMapController watches the k8sd-localpv-values ConfigMap and triggers
// local storage reconciliation whenever the ConfigMap is created, updated, or deleted.
type LocalPVConfigMapController struct {
	snap               snap.Snap
	notifyLocalStorage func()
}

// NewLocalPVConfigMapController creates a new LocalPVConfigMapController.
func NewLocalPVConfigMapController(snap snap.Snap, notifyLocalStorage func()) *LocalPVConfigMapController {
	return &LocalPVConfigMapController{
		snap:               snap,
		notifyLocalStorage: notifyLocalStorage,
	}
}

// Run starts the controller loop, retrying on error.
func (c *LocalPVConfigMapController) Run(ctx context.Context) {
	ctx = log.NewContext(ctx, log.FromContext(ctx).WithValues("controller", "localpv-configmap"))
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

func (c *LocalPVConfigMapController) watch(ctx context.Context) error {
	log := log.FromContext(ctx)

	client, err := getNewK8sClientWithRetries(ctx, c.snap, true)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	w, err := client.CoreV1().ConfigMaps("kube-system").Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=k8sd-localpv-values",
	})
	if err != nil {
		return fmt.Errorf("failed to watch ConfigMap: %w", err)
	}
	defer w.Stop()

	log.Info("Started watching k8sd-localpv-values ConfigMap")

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-w.ResultChan():
			if !ok {
				return fmt.Errorf("watcher channel closed")
			}
			if event.Type == watch.Error {
				status := apierrors.FromObject(event.Object)
				if apierrors.IsResourceExpired(status) {
					// Resource version too old — restart watch from latest.
					w.Stop()
					w, err = client.CoreV1().ConfigMaps("kube-system").Watch(ctx, metav1.ListOptions{
						FieldSelector: "metadata.name=k8sd-localpv-values",
					})
					if err != nil {
						if ctx.Err() != nil {
							return nil
						}
						return fmt.Errorf("failed to restart watch after resource expiry: %w", err)
					}
					continue
				}
				return fmt.Errorf("watch error event: %w", status)
			}
			log.Info("ConfigMap event received", "type", event.Type)
			c.notifyLocalStorage()
		}
	}
}
