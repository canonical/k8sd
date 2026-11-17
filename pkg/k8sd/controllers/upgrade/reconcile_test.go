package upgrade

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	upgradepkg "github.com/canonical/k8sd/pkg/upgrade"
	"github.com/canonical/k8sd/pkg/version"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type interceptedClient struct {
	ctrlclient.Client
	getFn    func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error
	listFn   func(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error
	statusFn func() ctrlclient.SubResourceWriter
}

func (c *interceptedClient) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if c.getFn != nil {
		return c.getFn(ctx, key, obj, opts...)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *interceptedClient) List(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	if c.listFn != nil {
		return c.listFn(ctx, list, opts...)
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *interceptedClient) Status() ctrlclient.SubResourceWriter {
	if c.statusFn != nil {
		return c.statusFn()
	}
	return c.Client.Status()
}

type interceptedStatusWriter struct {
	ctrlclient.SubResourceWriter
	patchFn func(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error
}

func (w *interceptedStatusWriter) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
	if w.patchFn != nil {
		return w.patchFn(ctx, obj, patch, opts...)
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func buildFakeClient(t *testing.T, initObjs ...ctrlclient.Object) ctrlclient.Client {
	t.Helper()
	g := NewWithT(t)

	scheme, err := kubernetes.NewScheme()
	g.Expect(err).ToNot(HaveOccurred())

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initObjs...).
		WithStatusSubresource(&upgradesv1alpha.Upgrade{}).
		Build()
}

func nodeWithVersion(t *testing.T, name string, revision string, k8sVerStr string) *corev1.Node {
	t.Helper()
	g := NewWithT(t)

	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: make(map[string]string),
		},
	}
	if revision != "" || k8sVerStr != "" {
		v := version.Info{
			Revision: revision,
		}
		if k8sVerStr != "" {
			v.KubernetesVersion = versionutil.MustParseSemantic(k8sVerStr)
		}
		b, err := v.Encode()
		g.Expect(err).ToNot(HaveOccurred())
		n.Annotations[version.NodeAnnotationKey] = string(b)
	}
	return n
}

func upgradeResource(t *testing.T, name string, phase upgradesv1alpha.UpgradePhase, upgradedNodes ...string) *upgradesv1alpha.Upgrade {
	t.Helper()
	return &upgradesv1alpha.Upgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: upgradesv1alpha.UpgradeStatus{
			Phase:         phase,
			UpgradedNodes: upgradedNodes,
		},
	}
}

func TestReconcile_TopLevelRoutingAndRequeue(t *testing.T) {
	t.Run("resource not found requeues with 5m", func(t *testing.T) {
		g := NewWithT(t)
		client := buildFakeClient(t)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "missing"},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{RequeueAfter: 5 * time.Minute}))
	})

	t.Run("actionable upgrade reconciles successfully and requeues with 5m", func(t *testing.T) {
		g := NewWithT(t)
		n1 := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		n2 := nodeWithVersion(t, "node-2", "42", "v1.30.0")
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseNodeUpgrade, "node-1", "node-2")

		client := buildFakeClient(t, n1, n2, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "up-1"},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{RequeueAfter: 5 * time.Minute}))

		fetched := &upgradesv1alpha.Upgrade{}
		g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: "up-1"}, fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseFeatureUpgrade))
	})

	t.Run("actionable node reconciles successfully and requeues with 5m", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		vInfo := version.Info{Revision: "42", KubernetesVersion: versionutil.MustParseSemantic("v1.30.0")}
		upName := upgradepkg.GetName(vInfo)
		u := upgradeResource(t, upName, upgradesv1alpha.UpgradePhaseNodeUpgrade)

		client := buildFakeClient(t, n, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "node-1"},
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{RequeueAfter: 5 * time.Minute}))

		fetched := &upgradesv1alpha.Upgrade{}
		g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: upName}, fetched)).To(Succeed())
		g.Expect(fetched.Status.UpgradedNodes).To(ContainElement("node-1"))
	})

	t.Run("aggregate error when both upgrade and node Get fail", func(t *testing.T) {
		g := NewWithT(t)
		baseClient := buildFakeClient(t)
		client := &interceptedClient{
			Client: baseClient,
			getFn: func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
				return errors.New("simulated network failure")
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "broken"},
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
		g.Expect(err.Error()).To(ContainSubstring("failed to get upgrade"))
		g.Expect(err.Error()).To(ContainSubstring("failed to get node"))
	})

	t.Run("error when upgrade Get fails and node is NotFound", func(t *testing.T) {
		g := NewWithT(t)
		baseClient := buildFakeClient(t)
		client := &interceptedClient{
			Client: baseClient,
			getFn: func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
				if _, ok := obj.(*upgradesv1alpha.Upgrade); ok {
					return errors.New("upgrade get error")
				}
				return baseClient.Get(ctx, key, obj, opts...)
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "missing-node"},
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
		g.Expect(err.Error()).To(ContainSubstring("upgrade get error"))
	})

	t.Run("error when node Get fails and upgrade is NotFound", func(t *testing.T) {
		g := NewWithT(t)
		baseClient := buildFakeClient(t)
		client := &interceptedClient{
			Client: baseClient,
			getFn: func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
				if _, ok := obj.(*corev1.Node); ok {
					return errors.New("node get error")
				}
				return baseClient.Get(ctx, key, obj, opts...)
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "missing-upgrade"},
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
		g.Expect(err.Error()).To(ContainSubstring("node get error"))
	})

	t.Run("wraps error when reconcileUpgrade returns error", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-fail", upgradesv1alpha.UpgradePhaseFailed)
		client := buildFakeClient(t, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "up-fail"},
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
		g.Expect(err.Error()).To(ContainSubstring("failed to reconcile: failed to reconcile upgrade"))
		g.Expect(err.Error()).To(ContainSubstring("upgrade \"up-fail\" has failed"))
	})

	t.Run("wraps error when reconcileNode returns error", func(t *testing.T) {
		g := NewWithT(t)
		n := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-bad-ann",
				Annotations: map[string]string{
					version.NodeAnnotationKey: "not-json",
				},
			},
		}
		client := buildFakeClient(t, n)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: k8stypes.NamespacedName{Name: "node-bad-ann"},
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
		g.Expect(err.Error()).To(ContainSubstring("failed to reconcile: failed to reconcile node"))
		g.Expect(err.Error()).To(ContainSubstring("failed to decode version info for node \"node-bad-ann\""))
	})
}

func TestReconcileNode(t *testing.T) {
	t.Run("missing version annotation skips reconciliation", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "", "")
		client := buildFakeClient(t, n)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
	})

	t.Run("invalid version annotation returns decode error", func(t *testing.T) {
		g := NewWithT(t)
		n := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-bad",
				Annotations: map[string]string{
					version.NodeAnnotationKey: "invalid-json",
				},
			},
		}
		client := buildFakeClient(t, n)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		_, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to decode version info for node \"node-bad\""))
	})

	t.Run("matching upgrade CR not found skips reconciliation", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		client := buildFakeClient(t, n)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
	})

	t.Run("matching upgrade CR Get fails with non-NotFound error", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		baseClient := buildFakeClient(t, n)
		client := &interceptedClient{
			Client: baseClient,
			getFn: func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
				if _, ok := obj.(*upgradesv1alpha.Upgrade); ok {
					return errors.New("server unreachable")
				}
				return baseClient.Get(ctx, key, obj, opts...)
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		_, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to get upgrade for version"))
	})

	t.Run("skips adding node when upgrade phase is not NodeUpgrade", func(t *testing.T) {
		tests := []struct {
			name  string
			phase upgradesv1alpha.UpgradePhase
		}{
			{name: "FeatureUpgrade", phase: upgradesv1alpha.UpgradePhaseFeatureUpgrade},
			{name: "Completed", phase: upgradesv1alpha.UpgradePhaseCompleted},
			{name: "Failed", phase: upgradesv1alpha.UpgradePhaseFailed},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				g := NewWithT(t)
				n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
				vInfo := version.Info{Revision: "42", KubernetesVersion: versionutil.MustParseSemantic("v1.30.0")}
				upName := upgradepkg.GetName(vInfo)
				u := upgradeResource(t, upName, tc.phase)

				client := buildFakeClient(t, n, u)
				c := NewController(logr.Discard(), client, ControllerOptions{})

				res, err := c.reconcileNode(context.Background(), n)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(res).To(Equal(ctrl.Result{}))

				// Verify node was NOT added to UpgradedNodes
				fetched := &upgradesv1alpha.Upgrade{}
				g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: upName}, fetched)).To(Succeed())
				g.Expect(fetched.Status.UpgradedNodes).To(BeEmpty())
			})
		}
	})

	t.Run("adds node to upgraded nodes when in NodeUpgrade phase", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		vInfo := version.Info{Revision: "42", KubernetesVersion: versionutil.MustParseSemantic("v1.30.0")}
		upName := upgradepkg.GetName(vInfo)
		u := upgradeResource(t, upName, upgradesv1alpha.UpgradePhaseNodeUpgrade, "existing-node")

		client := buildFakeClient(t, n, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))

		fetched := &upgradesv1alpha.Upgrade{}
		g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: upName}, fetched)).To(Succeed())
		g.Expect(fetched.Status.UpgradedNodes).To(ConsistOf("existing-node", "node-1"))
	})

	t.Run("idempotent when node is already in upgraded nodes list", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		vInfo := version.Info{Revision: "42", KubernetesVersion: versionutil.MustParseSemantic("v1.30.0")}
		upName := upgradepkg.GetName(vInfo)
		u := upgradeResource(t, upName, upgradesv1alpha.UpgradePhaseNodeUpgrade, "node-1")

		patchCalled := false
		baseClient := buildFakeClient(t, n, u)
		client := &interceptedClient{
			Client: baseClient,
			statusFn: func() ctrlclient.SubResourceWriter {
				return &interceptedStatusWriter{
					SubResourceWriter: baseClient.Status(),
					patchFn: func(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
						patchCalled = true
						return baseClient.Status().Patch(ctx, obj, patch, opts...)
					},
				}
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
		g.Expect(patchCalled).To(BeFalse(), "patch should not be called if node is already upgraded")
	})

	t.Run("returns error when status patch fails on addToUpgradedNodes", func(t *testing.T) {
		g := NewWithT(t)
		n := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		vInfo := version.Info{Revision: "42", KubernetesVersion: versionutil.MustParseSemantic("v1.30.0")}
		upName := upgradepkg.GetName(vInfo)
		u := upgradeResource(t, upName, upgradesv1alpha.UpgradePhaseNodeUpgrade)

		baseClient := buildFakeClient(t, n, u)
		client := &interceptedClient{
			Client: baseClient,
			statusFn: func() ctrlclient.SubResourceWriter {
				return &interceptedStatusWriter{
					SubResourceWriter: baseClient.Status(),
					patchFn: func(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
						return errors.New("conflict writing status")
					},
				}
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		_, err := c.reconcileNode(context.Background(), n)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to add node \"node-1\" to upgraded nodes: failed to patch"))
	})
}

func TestReconcileUpgrade_NodeUpgradePhase(t *testing.T) {
	t.Run("returns error when node listing fails", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseNodeUpgrade)
		baseClient := buildFakeClient(t, u)
		client := &interceptedClient{
			Client: baseClient,
			listFn: func(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
				return errors.New("list nodes failed")
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to check if all nodes have been upgraded: failed to list nodes"))
	})

	t.Run("remains in NodeUpgrade phase when some nodes are not upgraded", func(t *testing.T) {
		g := NewWithT(t)
		n1 := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		n2 := nodeWithVersion(t, "node-2", "42", "v1.30.0")
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseNodeUpgrade, "node-1")

		client := buildFakeClient(t, n1, n2, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))

		fetched := &upgradesv1alpha.Upgrade{}
		g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: "up-1"}, fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseNodeUpgrade))
	})

	t.Run("transitions to FeatureUpgrade phase when all nodes are upgraded", func(t *testing.T) {
		g := NewWithT(t)
		n1 := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		n2 := nodeWithVersion(t, "node-2", "42", "v1.30.0")
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseNodeUpgrade, "node-1", "node-2")

		client := buildFakeClient(t, n1, n2, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))

		fetched := &upgradesv1alpha.Upgrade{}
		g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: "up-1"}, fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseFeatureUpgrade))
	})

	t.Run("returns error when transitionTo patch fails", func(t *testing.T) {
		g := NewWithT(t)
		n1 := nodeWithVersion(t, "node-1", "42", "v1.30.0")
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseNodeUpgrade, "node-1")

		baseClient := buildFakeClient(t, n1, u)
		client := &interceptedClient{
			Client: baseClient,
			statusFn: func() ctrlclient.SubResourceWriter {
				return &interceptedStatusWriter{
					SubResourceWriter: baseClient.Status(),
					patchFn: func(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
						return errors.New("transition patch failed")
					},
				}
			},
		}
		c := NewController(logr.Discard(), client, ControllerOptions{})

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to transition to \"FeatureUpgrade\" phase: failed to patch"))
	})
}

func TestReconcileUpgrade_FeatureUpgradePhase(t *testing.T) {
	t.Run("times out waiting for feature controllers to be ready", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseFeatureUpgrade)
		client := buildFakeClient(t, u)

		readyCh := make(chan struct{}) // never closed
		opts := ControllerOptions{
			FeatureControllerReadyCh:      readyCh,
			FeatureControllerReadyTimeout: 10 * time.Millisecond,
		}
		c := NewController(logr.Discard(), client, opts)

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("timed out waiting for feature controllers to be ready"))
	})

	t.Run("fails when unknown feature is in FeatureToReconciledCh", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseFeatureUpgrade)
		client := buildFakeClient(t, u)

		readyCh := make(chan struct{})
		close(readyCh)
		reconciledCh := make(chan struct{})

		opts := ControllerOptions{
			FeatureControllerReadyCh: readyCh,
			FeatureToReconciledCh: map[types.FeatureName]<-chan struct{}{
				"unsupported-feature": reconciledCh,
			},
			FeatureControllerReadyTimeout:     5 * time.Second,
			FeatureControllerReconcileTimeout: 5 * time.Second,
		}
		c := NewController(logr.Discard(), client, opts)

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to wait for feature reconciliations: failed to trigger feature \"unsupported-feature\": unknown feature \"unsupported-feature\""))
	})

	t.Run("times out waiting for feature reconciliation", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseFeatureUpgrade)
		client := buildFakeClient(t, u)

		readyCh := make(chan struct{})
		close(readyCh)
		networkCh := make(chan struct{}) // never sends
		networkNotified := false

		opts := ControllerOptions{
			FeatureControllerReadyCh: readyCh,
			NotifyNetworkFeature:     func() { networkNotified = true },
			FeatureToReconciledCh: map[types.FeatureName]<-chan struct{}{
				features.Network: networkCh,
			},
			FeatureControllerReadyTimeout:     5 * time.Second,
			FeatureControllerReconcileTimeout: 10 * time.Millisecond,
		}
		c := NewController(logr.Discard(), client, opts)

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(networkNotified).To(BeTrue())
		g.Expect(err.Error()).To(ContainSubstring("failed to wait for feature reconciliations: timed out waiting for feature \"network\" to get reconciled"))
	})

	t.Run("context canceled while waiting for feature reconciliation", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseFeatureUpgrade)
		client := buildFakeClient(t, u)

		readyCh := make(chan struct{})
		close(readyCh)
		networkCh := make(chan struct{})

		opts := ControllerOptions{
			FeatureControllerReadyCh: readyCh,
			NotifyNetworkFeature:     func() {},
			FeatureToReconciledCh: map[types.FeatureName]<-chan struct{}{
				features.Network: networkCh,
			},
			FeatureControllerReadyTimeout:     5 * time.Second,
			FeatureControllerReconcileTimeout: 5 * time.Second,
		}
		c := NewController(logr.Discard(), client, opts)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // canceled immediately

		_, err := c.reconcileUpgrade(ctx, u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to wait for feature reconciliations: context canceled"))
	})

	t.Run("successfully reconciles all features and transitions to Completed", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseFeatureUpgrade)
		client := buildFakeClient(t, u)

		readyCh := make(chan struct{})
		close(readyCh)

		notified := make(map[types.FeatureName]bool)

		networkCh := make(chan struct{}, 1)
		gatewayCh := make(chan struct{}, 1)
		ingressCh := make(chan struct{}, 1)
		loadBalancerCh := make(chan struct{}, 1)
		localStorageCh := make(chan struct{}, 1)
		metricsServerCh := make(chan struct{}, 1)
		dnsCh := make(chan struct{}, 1)

		opts := ControllerOptions{
			FeatureControllerReadyCh: readyCh,
			NotifyNetworkFeature: func() {
				notified[features.Network] = true
				networkCh <- struct{}{}
			},
			NotifyGatewayFeature: func() {
				notified[features.Gateway] = true
				gatewayCh <- struct{}{}
			},
			NotifyIngressFeature: func() {
				notified[features.Ingress] = true
				ingressCh <- struct{}{}
			},
			NotifyLoadBalancerFeature: func() {
				notified[features.LoadBalancer] = true
				loadBalancerCh <- struct{}{}
			},
			NotifyLocalStorageFeature: func() {
				notified[features.LocalStorage] = true
				localStorageCh <- struct{}{}
			},
			NotifyMetricsServerFeature: func() {
				notified[features.MetricsServer] = true
				metricsServerCh <- struct{}{}
			},
			NotifyDNSFeature: func() {
				notified[features.DNS] = true
				dnsCh <- struct{}{}
			},
			FeatureToReconciledCh: map[types.FeatureName]<-chan struct{}{
				features.Network:       networkCh,
				features.Gateway:       gatewayCh,
				features.Ingress:       ingressCh,
				features.LoadBalancer:  loadBalancerCh,
				features.LocalStorage:  localStorageCh,
				features.MetricsServer: metricsServerCh,
				features.DNS:           dnsCh,
			},
			FeatureControllerReadyTimeout:     5 * time.Second,
			FeatureControllerReconcileTimeout: 5 * time.Second,
		}
		c := NewController(logr.Discard(), client, opts)

		res, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))

		allFeatures := []types.FeatureName{
			features.Network,
			features.Gateway,
			features.Ingress,
			features.LoadBalancer,
			features.LocalStorage,
			features.MetricsServer,
			features.DNS,
		}
		for _, feat := range allFeatures {
			g.Expect(notified[feat]).To(BeTrue(), fmt.Sprintf("expected %s to be notified", feat))
		}

		fetched := &upgradesv1alpha.Upgrade{}
		g.Expect(client.Get(context.Background(), ctrlclient.ObjectKey{Name: "up-1"}, fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(upgradesv1alpha.UpgradePhaseCompleted))
	})

	t.Run("returns error when transitionTo Completed patch fails", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-1", upgradesv1alpha.UpgradePhaseFeatureUpgrade)

		baseClient := buildFakeClient(t, u)
		client := &interceptedClient{
			Client: baseClient,
			statusFn: func() ctrlclient.SubResourceWriter {
				return &interceptedStatusWriter{
					SubResourceWriter: baseClient.Status(),
					patchFn: func(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
						return errors.New("completed patch failed")
					},
				}
			},
		}

		readyCh := make(chan struct{})
		close(readyCh)

		opts := ControllerOptions{
			FeatureControllerReadyCh:          readyCh,
			FeatureToReconciledCh:             map[types.FeatureName]<-chan struct{}{},
			FeatureControllerReadyTimeout:     5 * time.Second,
			FeatureControllerReconcileTimeout: 5 * time.Second,
		}
		c := NewController(logr.Discard(), client, opts)

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to transition to \"Completed\" phase: failed to patch"))
	})
}

func TestReconcileUpgrade_OtherPhases(t *testing.T) {
	t.Run("completed phase logs and returns bare result", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-done", upgradesv1alpha.UpgradePhaseCompleted)
		client := buildFakeClient(t, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
	})

	t.Run("failed phase returns error", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-broken", upgradesv1alpha.UpgradePhaseFailed)
		client := buildFakeClient(t, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		_, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(Equal("upgrade \"up-broken\" has failed"))
	})

	t.Run("unknown phase returns bare result", func(t *testing.T) {
		g := NewWithT(t)
		u := upgradeResource(t, "up-unknown", upgradesv1alpha.UpgradePhase("Pending"))
		client := buildFakeClient(t, u)
		c := NewController(logr.Discard(), client, ControllerOptions{})

		res, err := c.reconcileUpgrade(context.Background(), u)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res).To(Equal(ctrl.Result{}))
	})
}

func TestTriggerFeature(t *testing.T) {
	tests := []struct {
		name        types.FeatureName
		expectErr   bool
		expectedErr string
	}{
		{name: features.Network},
		{name: features.Gateway},
		{name: features.Ingress},
		{name: features.LoadBalancer},
		{name: features.LocalStorage},
		{name: features.MetricsServer},
		{name: features.DNS},
		{
			name:        types.FeatureName("non-existent"),
			expectErr:   true,
			expectedErr: `unknown feature "non-existent"`,
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.name), func(t *testing.T) {
			g := NewWithT(t)
			notified := make(map[types.FeatureName]bool)
			opts := ControllerOptions{
				NotifyNetworkFeature:       func() { notified[features.Network] = true },
				NotifyGatewayFeature:       func() { notified[features.Gateway] = true },
				NotifyIngressFeature:       func() { notified[features.Ingress] = true },
				NotifyLoadBalancerFeature:  func() { notified[features.LoadBalancer] = true },
				NotifyLocalStorageFeature:  func() { notified[features.LocalStorage] = true },
				NotifyMetricsServerFeature: func() { notified[features.MetricsServer] = true },
				NotifyDNSFeature:           func() { notified[features.DNS] = true },
			}
			c := NewController(logr.Discard(), buildFakeClient(t), opts)

			err := c.triggerFeature(tc.name)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedErr))
				g.Expect(notified).To(BeEmpty())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(notified).To(Equal(map[types.FeatureName]bool{tc.name: true}))
			}
		})
	}
}
