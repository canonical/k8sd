package kubernetes

import (
	"context"
	"fmt"

	"github.com/canonical/k8sd/pkg/log"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	applyv1 "k8s.io/client-go/applyconfigurations/core/v1"
)

func (c *Client) WatchConfigMap(ctx context.Context, namespace string, name string, reconcile func(configMap *v1.ConfigMap) error) error {
	log := log.FromContext(ctx).WithValues("namespace", namespace, "name", name)

	// Seed reconcile with the current state of the ConfigMap before starting
	// the watch. A bare Watch does not synthesise ADDED events for objects
	// that already exist when the watch is established, so without this seed
	// any consumer that starts after the ConfigMap was created would silently
	// miss the steady-state contents (e.g. --cluster-dns) until the next
	// server-side change.
	var resourceVersion string
	cm, err := c.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		log.Info("Seeding reconcile from existing ConfigMap", "resourceVersion", cm.ResourceVersion)
		if err := reconcile(cm); err != nil {
			return fmt.Errorf("initial reconcile failed: %w", err)
		}
		resourceVersion = cm.ResourceVersion
	case apierrors.IsNotFound(err):
		// ConfigMap does not exist yet; the watch will deliver ADDED when it is created.
		log.Info("ConfigMap not found, watching for creation")
	default:
		return fmt.Errorf("failed to get configmap namespace=%s name=%s: %w", namespace, name, err)
	}

	w, err := c.CoreV1().ConfigMaps(namespace).Watch(ctx, metav1.SingleObject(metav1.ObjectMeta{
		Name:            name,
		ResourceVersion: resourceVersion,
	}))
	if err != nil {
		return fmt.Errorf("failed to watch configmap namespace=%s name=%s: %w", namespace, name, err)
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-w.ResultChan():
			if !ok {
				return fmt.Errorf("watch closed")
			}
			if evt.Type == watch.Error {
				return fmt.Errorf("watch error event: %#v", evt.Object)
			}
			configMap, ok := evt.Object.(*v1.ConfigMap)
			if !ok {
				return fmt.Errorf("expected a ConfigMap but received %#v", evt.Object)
			}

			if err := reconcile(configMap); err != nil {
				log.Error(err, "Reconcile ConfigMap failed")
			}
		}
	}
}

func (c *Client) UpdateConfigMap(ctx context.Context, namespace string, name string, data map[string]string) (*v1.ConfigMap, error) {
	opts := applyv1.ConfigMap(name, namespace).WithData(data)
	configmap, err := c.CoreV1().ConfigMaps(namespace).Apply(ctx, opts, metav1.ApplyOptions{FieldManager: "ck-k8s-client"})
	if err != nil {
		return nil, fmt.Errorf("failed to update configmap, namespace: %s name: %s: %w", namespace, name, err)
	}
	return configmap, nil
}
