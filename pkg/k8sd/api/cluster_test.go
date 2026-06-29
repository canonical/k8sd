package api

import (
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	. "github.com/onsi/gomega"
)

func TestDeriveClusterHealth(t *testing.T) {
	tests := []struct {
		name     string
		ready    bool
		features []apiv2.FeatureStatus
		want     apiv2.ClusterHealth
	}{
		{
			name:  "nodes not ready always fails",
			ready: false,
			features: []apiv2.FeatureStatus{
				{State: apiv2.FeatureStateEnabled},
			},
			want: apiv2.ClusterHealthFailed,
		},
		{
			name:  "any failed feature fails the cluster",
			ready: true,
			features: []apiv2.FeatureStatus{
				{State: apiv2.FeatureStateEnabled},
				{State: apiv2.FeatureStateFailed},
				{State: apiv2.FeatureStateEnabled},
			},
			want: apiv2.ClusterHealthFailed,
		},
		{
			name:  "degraded or waiting feature degrades the cluster",
			ready: true,
			features: []apiv2.FeatureStatus{
				{State: apiv2.FeatureStateEnabled},
				{State: apiv2.FeatureStateWaiting},
			},
			want: apiv2.ClusterHealthDegraded,
		},
		{
			name:  "all enabled is ready",
			ready: true,
			features: []apiv2.FeatureStatus{
				{State: apiv2.FeatureStateEnabled},
				{State: apiv2.FeatureStateEnabled},
				{State: apiv2.FeatureStateDisabled},
			},
			want: apiv2.ClusterHealthReady,
		},
		{
			name:  "upgrade-window: empty State + Enabled true treated as Enabled",
			ready: true,
			features: []apiv2.FeatureStatus{
				{State: "", Enabled: true},
				{State: "", Enabled: false},
			},
			want: apiv2.ClusterHealthReady,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(deriveClusterHealth(tc.ready, tc.features)).To(Equal(tc.want))
		})
	}
}
