package controllers

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// helmOverrideFeature associates a ConfigMap name with the notify function that
// triggers re-reconciliation of the corresponding k8sd feature.
type helmOverrideFeature struct {
	configMapName string
	notify        func()
}

// HelmOverrideController watches the five feature-specific Helm override ConfigMaps
// in kube-system and notifies the FeatureController to re-apply the affected feature
// whenever a ConfigMap is created, updated, or deleted.
//
// All ConfigMaps share the naming convention k8sd-<feature>-values and carry a single
// "values" key containing a YAML fragment that is deep-merged with the feature defaults.
type HelmOverrideController struct {
	logger   logr.Logger
	client   client.Client
	features []helmOverrideFeature
}

// HelmOverrideControllerOptions configures the HelmOverrideController.
type HelmOverrideControllerOptions struct {
	// NotifyDNS triggers re-reconciliation of the CoreDNS feature.
	NotifyDNS func()
	// NotifyNetwork triggers re-reconciliation of the Cilium network feature.
	NotifyNetwork func()
	// NotifyLoadBalancer triggers re-reconciliation of the MetalLB load-balancer feature.
	NotifyLoadBalancer func()
	// NotifyLocalStorage triggers re-reconciliation of the LocalPV storage feature.
	NotifyLocalStorage func()
	// NotifyMetricsServer triggers re-reconciliation of the metrics-server feature.
	NotifyMetricsServer func()
	// Disable skips controller registration when true.
	Disable bool
}

// NewHelmOverrideController creates a new HelmOverrideController.
func NewHelmOverrideController(logger logr.Logger, c client.Client, opts HelmOverrideControllerOptions) *HelmOverrideController {
	return &HelmOverrideController{
		logger: logger,
		client: c,
		features: []helmOverrideFeature{
			{configMapName: "k8sd-coredns-values", notify: opts.NotifyDNS},
			{configMapName: "k8sd-cilium-values", notify: opts.NotifyNetwork},
			{configMapName: "k8sd-metallb-values", notify: opts.NotifyLoadBalancer},
			{configMapName: "k8sd-localpv-values", notify: opts.NotifyLocalStorage},
			{configMapName: "k8sd-metrics-server-values", notify: opts.NotifyMetricsServer},
		},
	}
}

// Reconcile is called by controller-runtime whenever one of the watched ConfigMaps changes.
// It looks up the matching feature and sends a notification to trigger re-reconciliation.
func (c *HelmOverrideController) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	for _, f := range c.features {
		if req.Name == f.configMapName {
			c.logger.Info("Helm override ConfigMap changed, notifying feature", "configmap", req.Name)
			if f.notify != nil {
				f.notify()
			}
			return ctrl.Result{}, nil
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the controller-runtime manager.
// Only ConfigMaps in kube-system matching one of the known override names are watched.
func (c *HelmOverrideController) SetupWithManager(mgr ctrl.Manager) error {
	knownNames := make(map[string]struct{}, len(c.features))
	for _, f := range c.features {
		knownNames[f.configMapName] = struct{}{}
	}

	isOverrideCM := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		_, known := knownNames[obj.GetName()]
		return obj.GetNamespace() == "kube-system" && known
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}, builder.WithPredicates(isOverrideCM)).
		Complete(c)
}
