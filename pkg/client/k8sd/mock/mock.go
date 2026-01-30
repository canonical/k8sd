package mock

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/k8sd"
)

// Mock is a mock implementation of k8sd.Client.
type Mock struct {
	// k8sd.ClusterClient
	BootstrapClusterCalledWith apiv2.BootstrapClusterRequest
	BootstrapClusterResponse   apiv2.BootstrapClusterResponse
	BootstrapClusterErr        error
	GetJoinTokenCalledWith     apiv2.GetJoinTokenRequest
	GetJoinTokenResponse       apiv2.GetJoinTokenResponse
	GetJoinTokenErr            error
	JoinClusterCalledWith      apiv2.JoinClusterRequest
	JoinClusterErr             error
	RemoveNodeCalledWith       apiv2.RemoveNodeRequest
	RemoveNodeErr              error

	// k8sd.StatusClient
	NodeStatusResponse    apiv2.NodeStatusResponse
	NodeStatusInitialized bool
	NodeStatusErr         error
	ClusterStatusResponse apiv2.ClusterStatusResponse
	ClusterStatusErr      error

	// k8sd.ConfigClient
	GetClusterConfigResponse   apiv2.GetClusterConfigResponse
	GetClusterConfigErr        error
	SetClusterConfigCalledWith apiv2.SetClusterConfigRequest
	SetClusterConfigErr        error

	// k8sd.ClusterMaintenanceClient
	RefreshCertificatesPlanCalledWith   apiv2.RefreshCertificatesPlanRequest
	RefreshCertificatesPlanResponse     apiv2.RefreshCertificatesPlanResponse
	RefreshCertificatesPlanErr          error
	RefreshCertificatesRunCalledWith    apiv2.RefreshCertificatesRunRequest
	RefreshCertificatesRunResponse      apiv2.RefreshCertificatesRunResponse
	RefreshCertificatesRunErr           error
	RefreshCertificatesUpdateCalledWith apiv2.RefreshCertificatesUpdateRequest
	RefreshCertificatesUpdateResponse   apiv2.RefreshCertificatesUpdateResponse
	RefreshCertificatesUpdateErr        error

	CertificatesStatusCalledWith apiv2.CertificatesStatusRequest
	CertificatesStatusResponse   apiv2.CertificatesStatusResponse
	CertificatesStatusErr        error

	// k8sd.UserClient
	KubeConfigCalledWith apiv2.KubeConfigRequest
	KubeConfigResponse   apiv2.KubeConfigResponse
	KubeConfigErr        error

	// k8sd.ClusterAPIClient
	SetClusterAPIAuthTokenCalledWith apiv2.ClusterAPISetAuthTokenRequest
	SetClusterAPIAuthTokenErr        error
}

func (m *Mock) BootstrapCluster(_ context.Context, request apiv2.BootstrapClusterRequest) (apiv2.BootstrapClusterResponse, error) {
	m.BootstrapClusterCalledWith = request
	return m.BootstrapClusterResponse, m.BootstrapClusterErr
}

func (m *Mock) GetJoinToken(_ context.Context, request apiv2.GetJoinTokenRequest) (apiv2.GetJoinTokenResponse, error) {
	m.GetJoinTokenCalledWith = request
	return m.GetJoinTokenResponse, m.GetJoinTokenErr
}

func (m *Mock) JoinCluster(_ context.Context, request apiv2.JoinClusterRequest) error {
	m.JoinClusterCalledWith = request
	return m.JoinClusterErr
}

func (m *Mock) RemoveNode(_ context.Context, request apiv2.RemoveNodeRequest) error {
	m.RemoveNodeCalledWith = request
	return m.RemoveNodeErr
}

func (m *Mock) NodeStatus(_ context.Context) (apiv2.NodeStatusResponse, bool, error) {
	return m.NodeStatusResponse, m.NodeStatusInitialized, m.NodeStatusErr
}

func (m *Mock) ClusterStatus(_ context.Context, waitReady bool) (apiv2.ClusterStatusResponse, error) {
	return m.ClusterStatusResponse, m.ClusterStatusErr
}

func (m *Mock) RefreshCertificatesPlan(_ context.Context, request apiv2.RefreshCertificatesPlanRequest) (apiv2.RefreshCertificatesPlanResponse, error) {
	return m.RefreshCertificatesPlanResponse, m.RefreshCertificatesPlanErr
}

func (m *Mock) RefreshCertificatesRun(_ context.Context, request apiv2.RefreshCertificatesRunRequest) (apiv2.RefreshCertificatesRunResponse, error) {
	return m.RefreshCertificatesRunResponse, m.RefreshCertificatesRunErr
}

func (m *Mock) RefreshCertificatesUpdate(_ context.Context, request apiv2.RefreshCertificatesUpdateRequest) (apiv2.RefreshCertificatesUpdateResponse, error) {
	m.RefreshCertificatesUpdateCalledWith = request
	return m.RefreshCertificatesUpdateResponse, m.RefreshCertificatesUpdateErr
}

func (m *Mock) CertificatesStatus(_ context.Context, request apiv2.CertificatesStatusRequest) (apiv2.CertificatesStatusResponse, error) {
	m.CertificatesStatusCalledWith = request
	return m.CertificatesStatusResponse, m.CertificatesStatusErr
}

func (m *Mock) GetClusterConfig(_ context.Context) (apiv2.GetClusterConfigResponse, error) {
	return m.GetClusterConfigResponse, m.GetClusterConfigErr
}

func (m *Mock) SetClusterConfig(_ context.Context, request apiv2.SetClusterConfigRequest) error {
	m.SetClusterConfigCalledWith = request
	return m.SetClusterConfigErr
}

func (m *Mock) KubeConfig(_ context.Context, request apiv2.KubeConfigRequest) (apiv2.KubeConfigResponse, error) {
	m.KubeConfigCalledWith = request
	return m.KubeConfigResponse, m.KubeConfigErr
}

func (m *Mock) SetClusterAPIAuthToken(_ context.Context, request apiv2.ClusterAPISetAuthTokenRequest) error {
	m.SetClusterAPIAuthTokenCalledWith = request
	return m.SetClusterAPIAuthTokenErr
}

var _ k8sd.Client = &Mock{}
