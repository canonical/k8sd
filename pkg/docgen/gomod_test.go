package docgen

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetGoPackageDirLocalModule(t *testing.T) {
	g := NewWithT(t)

	got, err := getGoPackageDir("github.com/canonical/k8sd/pkg/config", "/workspace/k8sd")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal(filepath.Join("/workspace/k8sd", "pkg", "config")))
}
