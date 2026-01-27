package impl

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/snap"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	nodeutil "github.com/canonical/k8sd/pkg/utils/node"
	"github.com/canonical/microcluster/v2/state"
)

// GetClusterMembers retrieves information about the members of the cluster.
func GetClusterMembers(ctx context.Context, s state.State) ([]apiv2.NodeStatus, error) {
	c, err := s.Leader()
	if err != nil {
		return nil, fmt.Errorf("failed to get leader client: %w", err)
	}

	clusterMembers, err := c.GetClusterMembers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster members: %w", err)
	}

	members := make([]apiv2.NodeStatus, len(clusterMembers))
	for i, clusterMember := range clusterMembers {
		members[i] = apiv2.NodeStatus{
			Name:          clusterMember.Name,
			Address:       clusterMember.Address.String(),
			ClusterRole:   apiv2.ClusterRoleControlPlane,
			DatastoreRole: nodeutil.DatastoreRoleFromString(clusterMember.Role),
		}
	}

	return members, nil
}

// GetLocalNodeStatus retrieves the status of the local node, including its roles within the cluster.
// Unlike "GetClusterMembers" this also works on a worker node.
func GetLocalNodeStatus(ctx context.Context, s state.State, snap snap.Snap) (apiv2.NodeStatus, error) {
	// Determine cluster role.
	clusterRole := apiv2.ClusterRoleUnknown
	isWorker, err := snaputil.IsWorker(snap)
	if err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to check if node is a worker: %w", err)
	}

	if isWorker {
		clusterRole = apiv2.ClusterRoleWorker
	} else if node, err := nodeutil.GetControlPlaneNode(ctx, s, s.Name()); err != nil {
		clusterRole = apiv2.ClusterRoleUnknown
	} else if node != nil {
		return *node, nil
	}

	return apiv2.NodeStatus{
		Name:        s.Name(),
		Address:     s.Address().Hostname(),
		ClusterRole: clusterRole,
	}, nil
}
