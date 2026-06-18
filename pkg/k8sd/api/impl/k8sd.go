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
	isWorker, err := snaputil.IsWorker(snap)
	if err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to check if node is a worker: %w", err)
	}

	status := apiv2.NodeStatus{
		Name:        s.Name(),
		Address:     s.Address().Hostname(),
		ClusterRole: apiv2.ClusterRoleUnknown,
	}

	if isWorker {
		status.ClusterRole = apiv2.ClusterRoleWorker
	} else if node, err := nodeutil.GetControlPlaneNode(ctx, s, s.Name(), snap); err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to get control plane node: %w", err)
	} else if err == nil && node != nil {
		status = *node
	}

	// Best-effort: enrich readiness and reachability from the Kubernetes API for
	// the local node. Use the node-scoped client so this also works on workers.
	// Failures here must not fail the whole request.
	if client, err := snap.KubernetesNodeClient(""); err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to create kubernetes node client for local node status: %w", err)
	} else if ns, err := client.GetNodeStatus(ctx, s.Name()); err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to get local node status from kubernetes, %w", err)
	} else {
		status.Ready = ns.Ready
		status.Reachable = ns.Reachable
	}

	return status, nil
}
