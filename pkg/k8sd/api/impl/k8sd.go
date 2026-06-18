package impl

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/snap"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	nodeutil "github.com/canonical/k8sd/pkg/utils/node"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// GetKubernetesNodes retrieves information about all the nodes in the k8s cluster.
func GetKubernetesNodes(ctx context.Context, s mctypes.State, snap snap.Snap, client *kubernetes.Client) ([]apiv2.NodeStatus, error) {
	c, err := snap.K8sdClient("")
	if err != nil {
		return nil, fmt.Errorf("failed to get k8sd client: %w", err)
	}

	k8sNode, err := client.ListNodesStatuses(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve all nodes statuses: %w", err)
	}

	members, err := c.GetClusterMembers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster members: %w", err)
	}

	datastoreRoleByName := make(map[string]string, len(members))
	for _, member := range members {
		datastoreRoleByName[member.Name] = member.Role
	}

	for i, node := range k8sNode {
		if node.ClusterRole == apiv2.ClusterRoleControlPlane {
			if role, ok := datastoreRoleByName[node.Name]; ok {
				k8sNode[i].DatastoreRole = nodeutil.DatastoreRoleFromString(role)
			}
		}
	}

	return k8sNode, nil
}

// GetLocalNodeStatus retrieves the status of the local node, including its roles within the cluster.
// Unlike "GetClusterMembers" this also works on a worker node.
func GetLocalNodeStatus(ctx context.Context, s mctypes.State, snap snap.Snap) (apiv2.NodeStatus, error) {
	// Determine cluster role.
	clusterRole := apiv2.ClusterRoleUnknown
	isWorker, err := snaputil.IsWorker(snap)
	if err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to check if node is a worker: %w", err)
	}

	if isWorker {
		clusterRole = apiv2.ClusterRoleWorker
	} else if node, err := nodeutil.GetControlPlaneNode(ctx, s, s.Name(), snap); err != nil {
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
