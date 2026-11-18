package types_test

import (
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	. "github.com/onsi/gomega"
)

func TestK8sdFeatureStatusToAPI(t *testing.T) {
	k8sdFS := types.FeatureStatus{
		Enabled:   true,
		Message:   "message",
		Version:   "version",
		UpdatedAt: time.Now(),
	}

	apiFS := k8sdFS.ToAPI()
	g := NewWithT(t)
	g.Expect(apiFS.Enabled).To(Equal(k8sdFS.Enabled))
	g.Expect(apiFS.Message).To(Equal(k8sdFS.Message))
	g.Expect(apiFS.Version).To(Equal(k8sdFS.Version))
	g.Expect(apiFS.UpdatedAt).To(Equal(k8sdFS.UpdatedAt))
}
