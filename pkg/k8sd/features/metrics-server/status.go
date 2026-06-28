package metrics_server

import (
	"context"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
)

func CheckMetricsServer(ctx context.Context, sn snap.Snap) types.ProbeResult {
	return types.ProbeResult{}
}
