package features_test

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

// TestStatusChecksWired is a phase-1 smoke test: every Check* in the
// StatusInterface must be wired to a non-nil implementation. A nil
// function-pointer field on the underlying statusChecks struct would
// pass compilation (the var is typed as the interface) and only panic
// at first call. The five stub probes below also confirm that the
// surface area is what we expect after phase 1: only CheckNetwork and
// CheckDNS hit the apiserver; the rest return an empty ProbeResult.
func TestStatusChecksWired(t *testing.T) {
	g := NewWithT(t)

	// Compile-time check (also enforced by the var declaration in
	// implementation_default.go, repeated here for documentation).
	var _ features.StatusInterface = features.StatusChecks

	g.Expect(features.StatusChecks).NotTo(BeNil())

	snapM := &snapmock.Snap{}
	ctx := context.Background()

	// Stubs only: these must not touch snap.KubernetesClient and must
	// return the zero ProbeResult to signal "no overlay".
	g.Expect(features.StatusChecks.CheckLoadBalancer(ctx, snapM)).To(Equal(types.ProbeResult{}))
	g.Expect(features.StatusChecks.CheckIngress(ctx, snapM)).To(Equal(types.ProbeResult{}))
	g.Expect(features.StatusChecks.CheckGateway(ctx, snapM)).To(Equal(types.ProbeResult{}))
	g.Expect(features.StatusChecks.CheckLocalStorage(ctx, snapM)).To(Equal(types.ProbeResult{}))
	g.Expect(features.StatusChecks.CheckMetricsServer(ctx, snapM)).To(Equal(types.ProbeResult{}))
}
