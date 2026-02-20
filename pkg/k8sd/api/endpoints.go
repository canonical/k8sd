// Package api provides the REST API endpoints.
package api

import (
	"context"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/microcluster/v3/microcluster/rest"
)

type Endpoints struct {
	context  context.Context
	provider Provider
}

// New creates a new API server instance.
// Context is the context to use for the API servers endpoints.
func New(ctx context.Context, provider Provider, drainConnectionsTimeout time.Duration) map[string]rest.Server {
	k8sd := &Endpoints{
		context:  ctx,
		provider: provider,
	}
	return map[string]rest.Server{
		"k8sd": {
			CoreAPI:   true,
			ServeUnix: true,
			PreInit:   true,
			Resources: []rest.Resources{
				{
					PathPrefix: apiv2.K8sdAPIVersion,
					Endpoints:  k8sd.Endpoints(),
				},
			},
			DrainConnectionsTimeout: drainConnectionsTimeout,
		},
	}
}

func (e *Endpoints) Context() context.Context {
	return e.context
}

// Endpoints returns the list of endpoints for a given microcluster app.
func (e *Endpoints) Endpoints() []rest.Endpoint {
	return []rest.Endpoint{
		// Cluster status and bootstrap
		{
			Name:              "Cluster",
			Path:              apiv2.BootstrapClusterRPC, // == apiv2.ClusterStatusRPC
			Get:               rest.EndpointAction{Handler: e.getClusterStatus, AccessHandler: e.restrictWorkers},
			Post:              rest.EndpointAction{Handler: e.postClusterBootstrap},
			AllowedBeforeInit: true,
		},
		// Node
		// Returns the status (e.g. current role) of the local node (control-plane, worker or unknown).
		{
			Name: "NodeStatus",
			Path: apiv2.NodeStatusRPC,
			Get:  rest.EndpointAction{Handler: e.getNodeStatus},
		},
		// Clustering
		// Unified token endpoint for both, control-plane and worker-node.
		{
			Name: "GetJoinToken",
			Path: apiv2.GetJoinTokenRPC,
			Post: rest.EndpointAction{Handler: e.postClusterJoinTokens, AccessHandler: e.restrictWorkers},
		},
		{
			Name: "JoinCluster",
			Path: apiv2.JoinClusterRPC,
			Post: rest.EndpointAction{Handler: e.postClusterJoin},
			// Joining a node is a bootstrapping action which needs to be available before k8sd is initialized.
			AllowedBeforeInit: true,
		},
		// Cluster removal (control-plane and worker nodes)
		{
			Name: "RemoveNode",
			Path: apiv2.RemoveNodeRPC,
			Post: rest.EndpointAction{Handler: e.postClusterRemove, AccessHandler: e.restrictWorkers},
		},
		// Worker nodes
		{
			Name: "GetWorkerJoinInfo",
			Path: apiv2.GetWorkerJoinInfoRPC,
			// AllowUntrusted disabled the microcluster authorization check. Authorization is done via custom token.
			Post: rest.EndpointAction{
				Handler:        e.postWorkerInfo,
				AllowUntrusted: true,
				AccessHandler:  ValidateWorkerInfoAccessHandler("Worker-Name", "Worker-Token"),
			},
		},
		// Certificates
		{
			Name: "RefreshCerts/Plan",
			Path: apiv2.RefreshCertificatesPlanRPC,
			Post: rest.EndpointAction{Handler: e.postRefreshCertsPlan},
		},
		{
			Name: "RefreshCerts/Run",
			Path: apiv2.RefreshCertificatesRunRPC,
			Post: rest.EndpointAction{Handler: e.postRefreshCertsRun},
		},
		{
			Name: "RefreshCerts/Update",
			Path: apiv2.RefreshCertificatesUpdateRPC,
			Post: rest.EndpointAction{Handler: e.postRefreshCertsUpdate},
		},
		{
			Name: "CertsStatus",
			Path: apiv2.CertificatesStatusRPC,
			Get:  rest.EndpointAction{Handler: e.getCertificatesStatus},
		},
		// Kubeconfig
		{
			Name: "Kubeconfig",
			Path: apiv2.KubeConfigRPC,
			Get:  rest.EndpointAction{Handler: e.getKubeconfig, AccessHandler: e.restrictWorkers},
		},
		// Get and modify the cluster configuration (e.g. to enable/disable features)
		{
			Name: "ClusterConfig",
			Path: apiv2.GetClusterConfigRPC, // == apiv2.SetClusterConfigRPC
			Put:  rest.EndpointAction{Handler: e.putClusterConfig, AccessHandler: e.restrictWorkers},
			Get:  rest.EndpointAction{Handler: e.getClusterConfig, AccessHandler: e.restrictWorkers},
		},
		// Kubernetes auth tokens and token review webhook for kube-apiserver
		{
			Name:   "KubernetesAuthTokens",
			Path:   apiv2.GenerateKubernetesAuthTokenRPC, // == apiv2.RevokeKubernetesAuthTokenRPC
			Post:   rest.EndpointAction{Handler: e.postKubernetesAuthTokens},
			Delete: rest.EndpointAction{Handler: e.deleteKubernetesAuthTokens},
		},
		{
			Name: "KubernetesAuthWebhook",
			Path: apiv2.ReviewKubernetesAuthTokenRPC,
			Post: rest.EndpointAction{Handler: e.postKubernetesAuthWebhook, AllowUntrusted: true},
		},
		// ClusterAPI management endpoints.
		{
			Name: "ClusterAPI/GetJoinToken",
			Path: apiv2.ClusterAPIGetJoinTokenRPC,
			Post: rest.EndpointAction{Handler: e.postClusterJoinTokens, AccessHandler: ValidateCAPIAuthTokenAccessHandler("capi-auth-token"), AllowUntrusted: true},
		},
		{
			Name: "ClusterAPI/SetAuthToken",
			Path: apiv2.ClusterAPISetAuthTokenRPC,
			Post: rest.EndpointAction{Handler: e.postSetClusterAPIAuthToken},
		},
		{
			Name: "ClusterAPI/RemoveNode",
			Path: apiv2.ClusterAPIRemoveNodeRPC,
			Post: rest.EndpointAction{Handler: e.postClusterRemove, AccessHandler: ValidateCAPIAuthTokenAccessHandler("capi-auth-token"), AllowUntrusted: true},
		},
		{
			Name: "ClusterAPI/CertificatesExpiry",
			Path: apiv2.ClusterAPICertificatesExpiryRPC,
			Post: rest.EndpointAction{Handler: e.postCertificatesExpiry, AccessHandler: e.ValidateNodeTokenAccessHandler("node-token"), AllowUntrusted: true},
		},
		{
			Name: "ClusterAPI/RefreshCerts/Plan",
			Path: apiv2.ClusterAPICertificatesPlanRPC,
			Post: rest.EndpointAction{Handler: e.postRefreshCertsPlan, AccessHandler: e.ValidateNodeTokenAccessHandler("node-token"), AllowUntrusted: true},
		},
		{
			Name: "ClusterAPI/RefreshCerts/Run",
			Path: apiv2.ClusterAPICertificatesRunRPC,
			Post: rest.EndpointAction{Handler: e.postRefreshCertsRun, AccessHandler: e.ValidateNodeTokenAccessHandler("node-token"), AllowUntrusted: true},
		},
		{
			Name: "ClusterAPI/RefreshCerts/Approve",
			Path: apiv2.ClusterAPIApproveWorkerCSRRPC,
			Post: rest.EndpointAction{Handler: e.postApproveWorkerCSR, AccessHandler: ValidateCAPIAuthTokenAccessHandler("capi-auth-token"), AllowUntrusted: true},
		},
		// Snap refreshes
		{
			Name: "Snap/Refresh",
			Path: apiv2.SnapRefreshRPC,
			Post: rest.EndpointAction{Handler: e.postSnapRefresh, AccessHandler: e.ValidateNodeTokenAccessHandler("node-token"), AllowUntrusted: true},
		},
		{
			Name: "Snap/RefreshStatus",
			Path: apiv2.SnapRefreshStatusRPC,
			Post: rest.EndpointAction{Handler: e.postSnapRefreshStatus, AccessHandler: e.ValidateNodeTokenAccessHandler("node-token"), AllowUntrusted: true},
		},
	}
}
