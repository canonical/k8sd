package features

import (
	"context"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
)

// StatusInterface defines the interface for checking the status of the built-in features.
type StatusInterface interface {
	// CheckDNS checks the status of the DNS feature.
	CheckDNS(context.Context, snap.Snap) types.ProbeResult
	// CheckNetwork checks the status of the Network feature.
	CheckNetwork(context.Context, snap.Snap) types.ProbeResult
	// CheckLoadBalancer checks the status of the Load Balancer feature.
	CheckLoadBalancer(context.Context, snap.Snap) types.ProbeResult
	// CheckIngress checks the status of the Ingress feature.
	CheckIngress(context.Context, snap.Snap) types.ProbeResult
	// CheckGateway checks the status of the Gateway feature.
	CheckGateway(context.Context, snap.Snap) types.ProbeResult
	// CheckLocalStorage checks the status of the Local Storage feature.
	CheckLocalStorage(context.Context, snap.Snap) types.ProbeResult
	// CheckMetricsServer checks the status of the Metrics Server feature.
	CheckMetricsServer(context.Context, snap.Snap) types.ProbeResult
}

// statusChecks implements the StatusInterface.
type statusChecks struct {
	checkDNS           func(context.Context, snap.Snap) types.ProbeResult
	checkNetwork       func(context.Context, snap.Snap) types.ProbeResult
	checkLoadBalancer  func(context.Context, snap.Snap) types.ProbeResult
	checkIngress       func(context.Context, snap.Snap) types.ProbeResult
	checkGateway       func(context.Context, snap.Snap) types.ProbeResult
	checkLocalStorage  func(context.Context, snap.Snap) types.ProbeResult
	checkMetricsServer func(context.Context, snap.Snap) types.ProbeResult
}

func (s *statusChecks) CheckDNS(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkDNS(ctx, snap)
}

func (s *statusChecks) CheckNetwork(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkNetwork(ctx, snap)
}

func (s *statusChecks) CheckLoadBalancer(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkLoadBalancer(ctx, snap)
}

func (s *statusChecks) CheckIngress(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkIngress(ctx, snap)
}

func (s *statusChecks) CheckGateway(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkGateway(ctx, snap)
}

func (s *statusChecks) CheckLocalStorage(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkLocalStorage(ctx, snap)
}

func (s *statusChecks) CheckMetricsServer(ctx context.Context, snap snap.Snap) types.ProbeResult {
	return s.checkMetricsServer(ctx, snap)
}
