package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/api/impl"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) getClusterStatus(s mctypes.State, r *http.Request) mctypes.Response {
	log := log.FromContext(r.Context()).WithValues("endpoint", "getClusterStatus")

	// fail if node is not initialized yet
	if err := s.Database().IsOpen(r.Context()); err != nil {
		return mctypes.Unavailable(fmt.Errorf("daemon not yet initialized"))
	}

	members, err := impl.GetClusterMembers(r.Context(), s, e.provider.Snap())
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get cluster members: %w", err))
	}
	config, err := databaseutil.GetClusterConfig(r.Context(), s)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get cluster config: %w", err))
	}

	client, err := e.provider.Snap().KubernetesClient("")
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to create k8s client: %w", err))
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

	return mctypes.SyncResponse(true, &apiv2.ClusterStatusResponse{
		ClusterStatus: apiv2.ClusterStatus{
			Ready:   ready,
			Members: members,
			Config:  config.ToUserFacing(),
			Datastore: apiv2.Datastore{
				Type:    config.Datastore.GetType(),
				Servers: config.Datastore.GetExternalServers(),
			},
			DNS:           statuses[features.DNS].ToAPI(),
			Network:       statuses[features.Network].ToAPI(),
			LoadBalancer:  statuses[features.LoadBalancer].ToAPI(),
			Ingress:       statuses[features.Ingress].ToAPI(),
			Gateway:       statuses[features.Gateway].ToAPI(),
			MetricsServer: statuses[features.MetricsServer].ToAPI(),
			LocalStorage:  statuses[features.LocalStorage].ToAPI(),
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
