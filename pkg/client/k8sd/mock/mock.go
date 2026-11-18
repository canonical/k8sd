package mock

import (
	"context"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/client/k8sd"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// Mock is a mock implementation of k8sd.Client.
type Mock struct {
	BootstrapClusterResponse        apiv2.BootstrapClusterResponse
	BootstrapClusterErr             error
	GetJoinTokenResponse            apiv2.GetJoinTokenResponse
	GetJoinTokenErr                 error
	JoinClusterErr                  error
	RemoveNodeErr                   error
	GetClusterMembersResponse       []mctypes.ClusterMember
	GetClusterMembersErr            error
	GetClusterMemberCalledWithNames []string
	GetClusterMemberFn              func(ctx context.Context, name string) (mctypes.ClusterMember, error)
	GetClusterMemberResponse        mctypes.ClusterMember
	GetClusterMemberErr             error
	RemoveClusterMemberErr          error

	NodeStatusResponse    apiv2.NodeStatusResponse
	NodeStatusInitialized bool
	NodeStatusErr         error
	ClusterStatusResponse apiv2.ClusterStatusResponse
	ClusterStatusErr      error

	GetClusterConfigResponse   apiv2.GetClusterConfigResponse
	GetClusterConfigErr        error
	SetClusterConfigCalledWith apiv2.SetClusterConfigRequest
	SetClusterConfigErr        error

	RefreshCertificatesPlanResponse   apiv2.RefreshCertificatesPlanResponse
	RefreshCertificatesPlanErr        error
	RefreshCertificatesRunResponse    apiv2.RefreshCertificatesRunResponse
	RefreshCertificatesRunErr         error
	RefreshCertificatesUpdateResponse apiv2.RefreshCertificatesUpdateResponse
	RefreshCertificatesUpdateErr      error

	CertificatesStatusResponse apiv2.CertificatesStatusResponse
	CertificatesStatusErr      error

	KubeConfigResponse apiv2.KubeConfigResponse
	KubeConfigErr      error

	SetClusterAPIAuthTokenErr error
}

func (m *Mock) BootstrapCluster(_ context.Context, _ apiv2.BootstrapClusterRequest) (apiv2.BootstrapClusterResponse, error) {
	return m.BootstrapClusterResponse, m.BootstrapClusterErr
}

func (m *Mock) GetJoinToken(_ context.Context, _ apiv2.GetJoinTokenRequest) (apiv2.GetJoinTokenResponse, error) {
	return m.GetJoinTokenResponse, m.GetJoinTokenErr
}

func (m *Mock) JoinCluster(_ context.Context, _ apiv2.JoinClusterRequest) error {
	return m.JoinClusterErr
}

func (m *Mock) RemoveNode(_ context.Context, _ apiv2.RemoveNodeRequest) error {
	return m.RemoveNodeErr
}

func (m *Mock) NodeStatus(_ context.Context) (apiv2.NodeStatusResponse, bool, error) {
	return m.NodeStatusResponse, m.NodeStatusInitialized, m.NodeStatusErr
}

func (m *Mock) ClusterStatus(_ context.Context, waitReady bool) (apiv2.ClusterStatusResponse, error) {
	return m.ClusterStatusResponse, m.ClusterStatusErr
}

func (m *Mock) RefreshCertificatesPlan(_ context.Context, _ apiv2.RefreshCertificatesPlanRequest) (apiv2.RefreshCertificatesPlanResponse, error) {
	return m.RefreshCertificatesPlanResponse, m.RefreshCertificatesPlanErr
}

func (m *Mock) RefreshCertificatesRun(_ context.Context, _ apiv2.RefreshCertificatesRunRequest) (apiv2.RefreshCertificatesRunResponse, error) {
	return m.RefreshCertificatesRunResponse, m.RefreshCertificatesRunErr
}

func (m *Mock) RefreshCertificatesUpdate(_ context.Context, _ apiv2.RefreshCertificatesUpdateRequest) (apiv2.RefreshCertificatesUpdateResponse, error) {
	return m.RefreshCertificatesUpdateResponse, m.RefreshCertificatesUpdateErr
}

func (m *Mock) CertificatesStatus(_ context.Context, _ apiv2.CertificatesStatusRequest) (apiv2.CertificatesStatusResponse, error) {
	return m.CertificatesStatusResponse, m.CertificatesStatusErr
}

func (m *Mock) GetClusterConfig(_ context.Context) (apiv2.GetClusterConfigResponse, error) {
	return m.GetClusterConfigResponse, m.GetClusterConfigErr
}

func (m *Mock) SetClusterConfig(_ context.Context, request apiv2.SetClusterConfigRequest) error {
	m.SetClusterConfigCalledWith = request
	return m.SetClusterConfigErr
}

func (m *Mock) KubeConfig(_ context.Context, _ apiv2.KubeConfigRequest) (apiv2.KubeConfigResponse, error) {
	return m.KubeConfigResponse, m.KubeConfigErr
}

func (m *Mock) SetClusterAPIAuthToken(_ context.Context, _ apiv2.ClusterAPISetAuthTokenRequest) error {
	return m.SetClusterAPIAuthTokenErr
}

func (m *Mock) GetClusterMembers(_ context.Context) ([]mctypes.ClusterMember, error) {
	return m.GetClusterMembersResponse, m.GetClusterMembersErr
}

func (m *Mock) GetClusterMember(ctx context.Context, name string) (mctypes.ClusterMember, error) {
	m.GetClusterMemberCalledWithNames = append(m.GetClusterMemberCalledWithNames, name)
	if m.GetClusterMemberFn != nil {
		return m.GetClusterMemberFn(ctx, name)
	}
	return m.GetClusterMemberResponse, m.GetClusterMemberErr
}

func (m *Mock) RemoveClusterMember(_ context.Context, _ string, _ string, _ bool) error {
	return m.RemoveClusterMemberErr
}

var _ k8sd.Client = &Mock{}
