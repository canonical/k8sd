package api

import (
	"context"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
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

func TestApplyNetworkDependencyWaiting(t *testing.T) {
	fs := func(s apiv2.FeatureState, m string) types.FeatureStatus {
		return types.FeatureStatus{State: s, Message: m}
	}
	waiting := fs(apiv2.FeatureStateWaiting, networkWaitingMessage)

	t.Run("relabels only Waiting dependents, preserves Failed/Degraded messages", func(t *testing.T) {
		g := NewWithT(t)
		in := map[types.FeatureName]types.FeatureStatus{
			features.Network:      fs(apiv2.FeatureStateFailed, "crash"),
			features.DNS:          fs(apiv2.FeatureStateWaiting, "0/2 ready"),         // pods can't start -> network
			features.LoadBalancer: fs(apiv2.FeatureStateFailed, "ImagePullBackOff"),   // own error: preserved
			features.Ingress:      fs(apiv2.FeatureStateDegraded, "could not verify"), // own error: preserved
			features.Gateway:      fs(apiv2.FeatureStateDisabled, ""),                 // disabled: untouched
		}
		applyNetworkDependencyWaiting(in)
		g.Expect(in).To(Equal(map[types.FeatureName]types.FeatureStatus{
			features.Network:      fs(apiv2.FeatureStateFailed, "crash"),
			features.DNS:          waiting,
			features.LoadBalancer: fs(apiv2.FeatureStateFailed, "ImagePullBackOff"),
			features.Ingress:      fs(apiv2.FeatureStateDegraded, "could not verify"),
			features.Gateway:      fs(apiv2.FeatureStateDisabled, ""),
		}))
	})

	t.Run("every unhealthy network state relabels a Waiting dependent", func(t *testing.T) {
		g := NewWithT(t)
		for _, s := range []apiv2.FeatureState{apiv2.FeatureStateFailed, apiv2.FeatureStateDegraded, apiv2.FeatureStateWaiting} {
			in := map[types.FeatureName]types.FeatureStatus{features.Network: fs(s, ""), features.DNS: fs(apiv2.FeatureStateWaiting, "own")}
			applyNetworkDependencyWaiting(in)
			g.Expect(in[features.DNS]).To(Equal(waiting), "network %q", s)
		}
	})

	t.Run("healthy or inactive network is a no-op", func(t *testing.T) {
		g := NewWithT(t)
		for _, s := range []apiv2.FeatureState{apiv2.FeatureStateEnabled, apiv2.FeatureStateDisabled, ""} {
			dns := fs(apiv2.FeatureStateWaiting, "own")
			in := map[types.FeatureName]types.FeatureStatus{features.Network: fs(s, ""), features.DNS: dns}
			applyNetworkDependencyWaiting(in)
			g.Expect(in[features.DNS]).To(Equal(dns), "network %q", s)
		}
	})
}

func TestIsHealthyOrInactive(t *testing.T) {
	g := NewWithT(t)
	for s, want := range map[apiv2.FeatureState]bool{
		apiv2.FeatureStateEnabled:  true,
		apiv2.FeatureStateDisabled: true,
		"":                         true,
		apiv2.FeatureStateFailed:   false,
		apiv2.FeatureStateDegraded: false,
		apiv2.FeatureStateWaiting:  false,
	} {
		g.Expect(isHealthyOrInactive(s)).To(Equal(want), "state %q", s)
	}
}

// fakeChecks implements features.StatusInterface, returning a canned
// ProbeResult per feature and ignoring the snap argument.
type fakeChecks map[types.FeatureName]types.ProbeResult

func (f fakeChecks) CheckDNS(context.Context, snap.Snap) types.ProbeResult { return f[features.DNS] }
func (f fakeChecks) CheckNetwork(context.Context, snap.Snap) types.ProbeResult {
	return f[features.Network]
}
func (f fakeChecks) CheckLoadBalancer(context.Context, snap.Snap) types.ProbeResult {
	return f[features.LoadBalancer]
}
func (f fakeChecks) CheckIngress(context.Context, snap.Snap) types.ProbeResult {
	return f[features.Ingress]
}
func (f fakeChecks) CheckGateway(context.Context, snap.Snap) types.ProbeResult {
	return f[features.Gateway]
}
func (f fakeChecks) CheckLocalStorage(context.Context, snap.Snap) types.ProbeResult {
	return f[features.LocalStorage]
}
func (f fakeChecks) CheckMetricsServer(context.Context, snap.Snap) types.ProbeResult {
	return f[features.MetricsServer]
}

func TestOverlayFeatureProbes(t *testing.T) {
	g := NewWithT(t)
	statuses := map[types.FeatureName]types.FeatureStatus{
		features.Network:      {State: apiv2.FeatureStateEnabled, Message: "old"}, // probed + overlaid
		features.DNS:          {State: apiv2.FeatureStateEnabled, Message: "old"}, // probe empty -> unchanged
		features.LoadBalancer: {State: apiv2.FeatureStateFailed, Message: "old"},  // not enabled -> not probed
	}
	checks := fakeChecks{
		features.Network:      {State: apiv2.FeatureStateFailed, Message: "crash"},
		features.DNS:          {}, // empty result -> no overlay
		features.LoadBalancer: {State: apiv2.FeatureStateEnabled, Message: "ignored"},
	}

	overlayFeatureProbes(context.Background(), nil, checks, statuses)

	g.Expect(statuses[features.Network]).To(Equal(types.FeatureStatus{State: apiv2.FeatureStateFailed, Message: "crash"}))
	g.Expect(statuses[features.DNS]).To(Equal(types.FeatureStatus{State: apiv2.FeatureStateEnabled, Message: "old"}))
	g.Expect(statuses[features.LoadBalancer]).To(Equal(types.FeatureStatus{State: apiv2.FeatureStateFailed, Message: "old"}))
}
