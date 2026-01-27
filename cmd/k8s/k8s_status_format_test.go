package k8s_test

import (
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/cmd/k8s"
	. "github.com/onsi/gomega"
)

func TestClusterStatusFormat(t *testing.T) {
	testCases := []struct {
		name           string
		clusterStatus  apiv2.ClusterStatus
		expectedOutput string
	}{
		{
			name: "Cluster ready, HA formed, nodes exist",
			clusterStatus: apiv2.ClusterStatus{
				Ready: true,
				Members: []apiv2.NodeStatus{
					{Name: "node1", DatastoreRole: apiv2.DatastoreRoleVoter, Address: "192.168.0.1", ClusterRole: apiv2.ClusterRoleControlPlane},
					{Name: "node2", DatastoreRole: apiv2.DatastoreRoleVoter, Address: "192.168.0.2", ClusterRole: apiv2.ClusterRoleControlPlane},
					{Name: "node3", DatastoreRole: apiv2.DatastoreRoleStandBy, Address: "192.168.0.3", ClusterRole: apiv2.ClusterRoleControlPlane},
				},
				Datastore:    apiv2.Datastore{Type: "etcd"},
				Network:      apiv2.FeatureStatus{Message: "enabled"},
				DNS:          apiv2.FeatureStatus{Message: "enabled at 192.168.0.10"},
				Ingress:      apiv2.FeatureStatus{Message: "enabled"},
				LoadBalancer: apiv2.FeatureStatus{Message: "enabled, L2 mode"},
				LocalStorage: apiv2.FeatureStatus{Message: "enabled at /var/snap/k8s/common/rawfile-storage"},
				Gateway:      apiv2.FeatureStatus{Message: "enabled"},
			},
			expectedOutput: `cluster status:           ready
control plane nodes:      192.168.0.1 (voter), 192.168.0.2 (voter), 192.168.0.3 (stand-by)
high availability:        no
datastore:                etcd
network:                  enabled
dns:                      enabled at 192.168.0.10
ingress:                  enabled
load-balancer:            enabled, L2 mode
local-storage:            enabled at /var/snap/k8s/common/rawfile-storage
gateway                   enabled`,
		},
		{
			name: "External Datastore",
			clusterStatus: apiv2.ClusterStatus{
				Ready: true,
				Members: []apiv2.NodeStatus{
					{Name: "node1", DatastoreRole: apiv2.DatastoreRoleVoter, Address: "192.168.0.1", ClusterRole: apiv2.ClusterRoleControlPlane},
				},
				Datastore: apiv2.Datastore{Type: "external", Servers: []string{"etcd-url1", "etcd-url2"}},
				Network:   apiv2.FeatureStatus{Message: "enabled"},
				DNS:       apiv2.FeatureStatus{Message: "enabled at 192.168.0.10"},
			},
			expectedOutput: `cluster status:           ready
control plane nodes:      192.168.0.1 (voter)
high availability:        no
datastore:                external
network:                  enabled
dns:                      enabled at 192.168.0.10
ingress:                  disabled
load-balancer:            disabled
local-storage:            disabled
gateway                   disabled`,
		},
		{
			name: "Cluster not ready, HA not formed, no nodes",
			clusterStatus: apiv2.ClusterStatus{
				Ready:     false,
				Members:   []apiv2.NodeStatus{},
				Config:    apiv2.UserFacingClusterConfig{},
				Datastore: apiv2.Datastore{},
			},
			expectedOutput: `cluster status:           not ready
control plane nodes:      none
high availability:        no
datastore:                disabled
network:                  disabled
dns:                      disabled
ingress:                  disabled
load-balancer:            disabled
local-storage:            disabled
gateway                   disabled`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(k8s.ClusterStatus(tc.clusterStatus).String()).To(Equal(tc.expectedOutput))
		})
	}
}
