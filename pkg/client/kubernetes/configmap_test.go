package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestWatchConfigMap(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		configmap *corev1.ConfigMap
	}{
		{
			name: "example configmap with values",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "test-config", Namespace: "kube-system"},
				Data: map[string]string{
					"non-existent-key1": "value1",
					"non-existent-key2": "value2",
					"non-existent-key3": "value3",
				},
			},
		},
		{
			name: "configmap with empty data",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "test-config", Namespace: "kube-system"},
				Data:       map[string]string{},
			},
		},
	}

	clientset := fake.NewSimpleClientset()
	watcher := watch.NewFake()
	clientset.PrependWatchReactor("configmaps", k8stesting.DefaultWatchReactor(watcher, nil))

	client := &Client{Interface: clientset}

	doneCh := make(chan *corev1.ConfigMap)

	go client.WatchConfigMap(ctx, "kube-system", "test-config", func(configMap *corev1.ConfigMap) error {
		doneCh <- configMap
		return nil
	})

	defer watcher.Stop()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			watcher.Add(tc.configmap)
			select {
			case recv := <-doneCh:
				g.Expect(recv.Data).To(Equal(tc.configmap.Data))
				g.Expect(recv.Name).To(Equal(tc.configmap.Name))
				g.Expect(recv.Namespace).To(Equal(tc.configmap.Namespace))
			case <-time.After(time.Second):
				t.Fatal("Timed out waiting for watch to complete")
			}
		})
	}
}

// TestWatchConfigMap_SeedsExistingObject verifies that when WatchConfigMap is
// invoked against a ConfigMap that already exists at the time the watch is
// established, reconcile is still called with its current contents. This is
// the real-world scenario on every node that joins or restarts after the
// k8sd-config ConfigMap has already been written by an earlier reconciliation:
// without an explicit Get-then-Watch seed, the Kubernetes API server does not
// synthesise an ADDED event for pre-existing objects on a cold watch, and the
// controller never reconciles kubelet with the existing --cluster-dns value.
func TestWatchConfigMap_SeedsExistingObject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := NewWithT(t)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
		Data: map[string]string{
			"cluster-dns":    "10.152.183.10",
			"cluster-domain": "cluster.local",
		},
	}

	clientset := fake.NewSimpleClientset(existing)
	client := &Client{Interface: clientset}

	doneCh := make(chan *corev1.ConfigMap, 1)
	go func() {
		_ = client.WatchConfigMap(ctx, "kube-system", "k8sd-config", func(cm *corev1.ConfigMap) error {
			doneCh <- cm
			return nil
		})
	}()

	select {
	case recv := <-doneCh:
		g.Expect(recv).ToNot(BeNil())
		g.Expect(recv.Name).To(Equal(existing.Name))
		g.Expect(recv.Namespace).To(Equal(existing.Namespace))
		g.Expect(recv.Data).To(Equal(existing.Data))
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile was not invoked for the pre-existing ConfigMap; WatchConfigMap missed the initial state")
	}
}

// TestWatchConfigMap_SeedErrorPropagates verifies that a reconcile failure
// during the initial seed causes WatchConfigMap to return an error rather than
// silently continuing. The caller's reconnect loop (NodeConfigurationController)
// depends on this to retry; if the error were swallowed, a transient failure
// during seeding would leave the node permanently misconfigured without any
// retry path (the ConfigMap's steady-state contents never change, so no further
// watch events arrive to trigger re-reconciliation).
func TestWatchConfigMap_SeedErrorPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := NewWithT(t)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
		Data:       map[string]string{"cluster-dns": "10.152.183.10"},
	}

	clientset := fake.NewSimpleClientset(existing)
	client := &Client{Interface: clientset}

	seedErr := fmt.Errorf("transient reconcile failure")

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.WatchConfigMap(ctx, "kube-system", "k8sd-config", func(*corev1.ConfigMap) error {
			return seedErr
		})
	}()

	select {
	case err := <-errCh:
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring(seedErr.Error()))
	case <-time.After(2 * time.Second):
		t.Fatal("WatchConfigMap did not return after seed reconcile failure")
	}
}

func TestUpdateConfigMap(t *testing.T) {
	ctx := context.Background()

	g := NewWithT(t)

	existingObjs := []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test-config", Namespace: "kube-system"},
			Data: map[string]string{
				"existing-key": "old-value",
			},
		},
	}

	clientset := fake.NewSimpleClientset(existingObjs...)
	client := &Client{Interface: clientset}

	updateData := map[string]string{
		"existing-key": "change-value",
		"new-key":      "new-value",
	}
	cm, err := client.UpdateConfigMap(ctx, "kube-system", "test-config", updateData)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cm.Data).To(Equal(updateData))
}
