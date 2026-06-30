package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/api/impl"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	"golang.org/x/sync/errgroup"
)

// featureProbeTimeout caps how long a single feature probe can run before
// it is cancelled. The probe is expected to translate the cancellation
// into a Degraded ProbeResult; the overlay does not synthesise one.
const featureProbeTimeout = 2 * time.Second

func (e *Endpoints) getClusterStatus(s mctypes.State, r *http.Request) mctypes.Response {
	log := log.FromContext(r.Context()).WithValues("endpoint", "getClusterStatus")

	// fail if node is not initialized yet
	if err := s.Database().IsOpen(r.Context()); err != nil {
		return mctypes.Unavailable(fmt.Errorf("daemon not yet initialized"))
	}

	client, err := e.provider.Snap().KubernetesClient("")
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to create k8s client: %w", err))
	}

	k8sNodes, err := impl.GetKubernetesNodes(r.Context(), s, e.provider.Snap(), client)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get cluster members: %w", err))
	}

	config, err := databaseutil.GetClusterConfig(r.Context(), s)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get cluster config: %w", err))
	}

	ready, err := client.HasReadyNodes(r.Context())
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to check if cluster has ready nodes: %w", err))
	}

	// If dns is enabled, we also check for the coredns service clusterIP before reporting cluster as "ready"
	if config.DNS.Enabled != nil && *config.DNS.Enabled {
		if err := e.checkKubeletClusterDNS(r.Context(), client); err != nil {
			log.Error(err, "kubelet does not have correct --cluster-dns arg")
			ready = false
		}
	}

	var statuses map[types.FeatureName]types.FeatureStatus
	if err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		var err error
		statuses, err = database.GetFeatureStatuses(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("failed to get feature statuses: %w", err)
		}
		return nil
	}); err != nil {
		return mctypes.InternalError(fmt.Errorf("database transaction failed: %w", err))
	}

	// Overlay live probe results on top of the DB-persisted statuses. Only
	// features that are currently Enabled in the DB are probed; Failed and
	// Disabled rows are already authoritative from the last Apply*.
	overlayFeatureProbes(r.Context(), e.provider.Snap(), features.StatusChecks, statuses)

	featureList := []apiv2.FeatureStatus{
		statuses[features.DNS].ToAPI(),
		statuses[features.Network].ToAPI(),
		statuses[features.LoadBalancer].ToAPI(),
		statuses[features.Ingress].ToAPI(),
		statuses[features.Gateway].ToAPI(),
		statuses[features.MetricsServer].ToAPI(),
		statuses[features.LocalStorage].ToAPI(),
	}

	return mctypes.SyncResponse(true, &apiv2.ClusterStatusResponse{
		ClusterStatus: apiv2.ClusterStatus{
			Ready:   ready,
			Status:  deriveClusterHealth(ready, featureList),
			Members: k8sNodes,
			Config:  config.ToUserFacing(),
			IsHA:    impl.IsHighlyAvailable(k8sNodes),
			Datastore: apiv2.Datastore{
				Type:    config.Datastore.GetType(),
				Servers: config.Datastore.GetExternalServers(),
			},
			DNS:           featureList[0],
			Network:       featureList[1],
			LoadBalancer:  featureList[2],
			Ingress:       featureList[3],
			Gateway:       featureList[4],
			MetricsServer: featureList[5],
			LocalStorage:  featureList[6],
		},
	})
}

// checkKubeletClusterDNS checks if --cluster-dns argument of the running kubelet service
// matches the coredns service clusterIP.
func (e *Endpoints) checkKubeletClusterDNS(ctx context.Context, client *kubernetes.Client) error {
	// this is similar to what we do in the coredns feature to get the cluster IP and update kubelet.
	// note that this is a bit brittle and might break if we change e.g. the coredns service name or namespace.
	corednsClusterIP, err := client.GetServiceClusterIP(ctx, "coredns", "kube-system")
	if err != nil {
		return fmt.Errorf("failed to get coredns service cluster IP: %w", err)
	}

	if corednsClusterIP == "" {
		return errors.New("coredns does not have a cluster IP yet")
	}

	serviceArgs, err := utils.RunningServiceArgs(ctx, "kubelet")
	if err != nil {
		return fmt.Errorf("failed to get args for kubelet: %w", err)
	}

	argsDNS := serviceArgs["--cluster-dns"]

	if argsDNS != corednsClusterIP {
		return fmt.Errorf("kubelet --cluster-dns %q does not match coredns service clusterIP %q", argsDNS, corednsClusterIP)
	}

	return nil
}

// deriveClusterHealth maps node readiness and per-feature state into a
// single cluster-wide health verdict.
func deriveClusterHealth(ready bool, featureList []apiv2.FeatureStatus) apiv2.ClusterHealth {
	if !ready {
		return apiv2.ClusterHealthFailed
	}

	hasDegraded := false
	for _, f := range featureList {
		switch f.State {
		case apiv2.FeatureStateFailed:
			return apiv2.ClusterHealthFailed
		case apiv2.FeatureStateDegraded, apiv2.FeatureStateWaiting:
			hasDegraded = true
		}
	}
	if hasDegraded {
		return apiv2.ClusterHealthDegraded
	}
	return apiv2.ClusterHealthReady
}

// overlayFeatureProbes runs the configured Check* probe for every feature
// whose persisted state is Enabled, then overlays the probe's State and
// Message onto the corresponding entry in `statuses` in place.
func overlayFeatureProbes(
	ctx context.Context,
	sn snap.Snap,
	checks features.StatusInterface,
	statuses map[types.FeatureName]types.FeatureStatus,
) {
	probes := []struct {
		name  types.FeatureName
		check func(context.Context, snap.Snap) types.ProbeResult
	}{
		{features.DNS, checks.CheckDNS},
		{features.Network, checks.CheckNetwork},
		{features.LoadBalancer, checks.CheckLoadBalancer},
		{features.Ingress, checks.CheckIngress},
		{features.Gateway, checks.CheckGateway},
		{features.LocalStorage, checks.CheckLocalStorage},
		{features.MetricsServer, checks.CheckMetricsServer},
	}

	results := make(map[types.FeatureName]types.ProbeResult, len(probes))
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	for _, p := range probes {
		cur, ok := statuses[p.name]
		if !ok || cur.State != apiv2.FeatureStateEnabled {
			continue
		}

		g.Go(func() error {
			pctx, cancel := context.WithTimeout(gctx, featureProbeTimeout)
			defer cancel()
			res := p.check(pctx, sn)
			mu.Lock()
			results[p.name] = res
			mu.Unlock()
			return nil
		})
	}
	// Callbacks never return an error; Wait is used only to fan-in.
	_ = g.Wait()

	for name, res := range results {
		if res.State == "" {
			continue
		}
		fs := statuses[name]
		fs.State = res.State
		fs.Message = res.Message
		statuses[name] = fs
	}
}
