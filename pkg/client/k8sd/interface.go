package k8sd

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

// ClusterClient implements methods for managing the cluster members.
type ClusterClient interface {
	// BootstrapCluster initializes a new cluster using the provided configuration.
	BootstrapCluster(context.Context, apiv2.BootstrapClusterRequest) (apiv2.BootstrapClusterResponse, error)
	// GetJoinToken generates a token for nodes to join the cluster.
	GetJoinToken(context.Context, apiv2.GetJoinTokenRequest) (apiv2.GetJoinTokenResponse, error)
	// JoinCluster joins an existing cluster.
	JoinCluster(context.Context, apiv2.JoinClusterRequest) error
	// RemoveNode removes a node from the cluster.
	RemoveNode(context.Context, apiv2.RemoveNodeRequest) error
}

// StatusClient implements methods for retrieving the current status of the cluster.
type StatusClient interface {
	// NodeStatus retrieves the current status of the local node.
	// The second return value is false if the node is not part of a cluster.
	NodeStatus(ctx context.Context) (apiv2.NodeStatusResponse, bool, error)
	// ClusterStatus retrieves the current status of the Kubernetes cluster.
	ClusterStatus(ctx context.Context, waitReady bool) (apiv2.ClusterStatusResponse, error)
}

// ConfigClient implements methods to retrieve and manage the cluster configuration.
type ConfigClient interface {
	// GetClusterConfig retrieves the k8sd cluster configuration.
	GetClusterConfig(context.Context) (apiv2.GetClusterConfigResponse, error)
	// SetClusterConfig updates the k8sd cluster configuration.
	SetClusterConfig(context.Context, apiv2.SetClusterConfigRequest) error
}

// ClusterMaintenanceClient implements methods to manage the cluster.
type ClusterMaintenanceClient interface {
	// RefreshCertificatesPlan generates a plan to refresh the Kubernetes certificates of the node.
	RefreshCertificatesPlan(context.Context, apiv2.RefreshCertificatesPlanRequest) (apiv2.RefreshCertificatesPlanResponse, error)
	// RefreshCertificatesRun refreshes the Kubernetes certificates of the node.
	RefreshCertificatesRun(context.Context, apiv2.RefreshCertificatesRunRequest) (apiv2.RefreshCertificatesRunResponse, error)
	// RefreshCertificatesUpdate updates the Kubernetes certificates of the node.
	RefreshCertificatesUpdate(context.Context, apiv2.RefreshCertificatesUpdateRequest) (apiv2.RefreshCertificatesUpdateResponse, error)
	// CertificatesStatus shows the status of the node's certificates.
	CertificatesStatus(context.Context, apiv2.CertificatesStatusRequest) (apiv2.CertificatesStatusResponse, error)
}

// UserClient implements methods to enable accessing the cluster.
type UserClient interface {
	// KubeConfig retrieves a kubeconfig file that can be used to access the cluster.
	KubeConfig(context.Context, apiv2.KubeConfigRequest) (apiv2.KubeConfigResponse, error)
}

// ClusterAPIClient implements methods related to ClusterAPI endpoints.
type ClusterAPIClient interface {
	// SetClusterAPIAuthToken sets the well-known token that can be used authenticating requests to the ClusterAPI related endpoints.
	SetClusterAPIAuthToken(context.Context, apiv2.ClusterAPISetAuthTokenRequest) error
}

type Client interface {
	ClusterClient
	StatusClient
	ConfigClient
	ClusterMaintenanceClient
	UserClient
	ClusterAPIClient
}
