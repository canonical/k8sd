package localpv_test

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/features/localpv"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

// TestCheckLocalStorageStub asserts the phase-1 stub returns an empty
// ProbeResult, signalling "no overlay" to the status endpoint. A real
// probe will replace it in a follow-up.
func TestCheckLocalStorageStub(t *testing.T) {
	g := NewWithT(t)
	snapM := &snapmock.Snap{}
	got := localpv.CheckLocalStorage(context.Background(), snapM)
	g.Expect(got).To(Equal(types.ProbeResult{}))
}
