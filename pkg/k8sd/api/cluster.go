package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/api/impl"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) getClusterStatus(s mctypes.State, r *http.Request) mctypes.Response {
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
