package node

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/microcluster/v2/state"
)

// GetControlPlaneNode returns the node information if the given node name
// belongs to a control-plane in the cluster or nil if not.
func GetControlPlaneNode(ctx context.Context, s state.State, name string) (*apiv2.NodeStatus, error) {
	client, err := s.Leader()
	if err != nil {
		return nil, fmt.Errorf("failed to get microcluster leader client: %w", err)
	}

	members, err := client.GetClusterMembers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get microcluster members: %w", err)
	}

	for _, member := range members {
		if member.Name == name {
			return &apiv2.NodeStatus{
				Name:          member.Name,
				Address:       member.Address.String(),
				ClusterRole:   apiv2.ClusterRoleControlPlane,
				DatastoreRole: DatastoreRoleFromString(member.Role),
			}, nil
		}
	}
	return nil, nil
}

// IsControlPlaneNode returns true if the given node name belongs to a control-plane node in the cluster.
func IsControlPlaneNode(ctx context.Context, s state.State, name string) (bool, error) {
	node, err := GetControlPlaneNode(ctx, s, name)
	if err != nil {
		return false, fmt.Errorf("failed to get control-plane node: %w", err)
	}
	return node != nil, nil
}

// DatastoreRoleFromString converts the string-based role to the enum-based role.
func DatastoreRoleFromString(role string) apiv2.DatastoreRole {
	switch role {
	case "voter":
		return apiv2.DatastoreRoleVoter
	case "stand-by":
		return apiv2.DatastoreRoleStandBy
	case "spare":
		return apiv2.DatastoreRoleSpare
	case "PENDING":
		return apiv2.DatastoreRolePending
	default:
		return apiv2.DatastoreRoleUnknown
	}
}
