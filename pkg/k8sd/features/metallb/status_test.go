package metallb_test

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/features/metallb"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

// TestCheckLoadBalancerStub asserts the phase-1 stub returns an empty
// ProbeResult, signalling "no overlay" to the status endpoint. A real
// probe will replace it in a follow-up.
func TestCheckLoadBalancerStub(t *testing.T) {
	g := NewWithT(t)
	snapM := &snapmock.Snap{}
	got := metallb.CheckLoadBalancer(context.Background(), snapM)
	g.Expect(got).To(Equal(types.ProbeResult{}))
}
