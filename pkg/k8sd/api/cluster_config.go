package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/database"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) putClusterConfig(s mctypes.State, r *http.Request) mctypes.Response {
	var req apiv2.SetClusterConfigRequest

	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to decode request: %w", err))
	}

	requestedConfig, err := types.ClusterConfigFromUserFacing(req.Config)
	if err != nil {
		return mctypes.BadRequest(fmt.Errorf("invalid configuration: %w", err))
	}
	if requestedConfig.Datastore, err = types.DatastoreConfigFromUserFacing(req.Datastore); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse datastore config: %w", err))
	}

	if err := s.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := database.SetClusterConfig(ctx, tx, requestedConfig); err != nil {
			return fmt.Errorf("failed to update cluster configuration: %w", err)
		}
		return nil
	}); err != nil {
		return mctypes.InternalError(fmt.Errorf("database transaction to update cluster configuration failed: %w", err))
	}

	e.provider.NotifyUpdateNodeConfigController()
	e.provider.NotifyFeatureController(
		!requestedConfig.Network.Empty(),
		!requestedConfig.Gateway.Empty(),
		!requestedConfig.Ingress.Empty(),
		!requestedConfig.LoadBalancer.Empty(),
		!requestedConfig.LocalStorage.Empty(),
		!requestedConfig.MetricsServer.Empty(),
		!requestedConfig.DNS.Empty() || !requestedConfig.Kubelet.Empty(),
	)

	return mctypes.SyncResponse(true, &apiv2.SetClusterConfigResponse{})
}

func (e *Endpoints) getClusterConfig(s mctypes.State, r *http.Request) mctypes.Response {
	config, err := databaseutil.GetClusterConfig(r.Context(), s)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to retrieve cluster configuration: %w", err))
	}

	return mctypes.SyncResponse(true, &apiv2.GetClusterConfigResponse{
		Config:      config.ToUserFacing(),
		Datastore:   config.Datastore.ToUserFacing(),
		PodCIDR:     config.Network.PodCIDR,
		ServiceCIDR: config.Network.ServiceCIDR,
	})
}
