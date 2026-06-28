package metrics_server_test

import (
	"context"
	"testing"

	metrics_server "github.com/canonical/k8sd/pkg/k8sd/features/metrics-server"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

// TestCheckMetricsServerStub asserts the phase-1 stub returns an empty
// ProbeResult, signalling "no overlay" to the status endpoint. A real
// probe will replace it in a follow-up.
func TestCheckMetricsServerStub(t *testing.T) {
	g := NewWithT(t)
	snapM := &snapmock.Snap{}
	got := metrics_server.CheckMetricsServer(context.Background(), snapM)
	g.Expect(got).To(Equal(types.ProbeResult{}))
}
