package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	netnsutils "github.com/canonical/k8sd/pkg/utils/netns"
	. "github.com/onsi/gomega"
)

func TestRemoveNetworkNamespacesOnlyRemovesOwned(t *testing.T) {
	g := NewWithT(t)
	netnsDir := t.TempDir()
	helper := netnsutils.NewMockNetworkNSHelper(netnsDir)

	for _, ns := range []string{"cni-owned", "cni-foreign", "owned-not-cni"} {
		g.Expect(os.WriteFile(filepath.Join(netnsDir, ns), nil, 0o600)).To(Succeed())
	}

	owned := NetworkNamespaces{}
	owned.add(filepath.Join(netnsDir, "cni-owned"))
	owned.add(filepath.Join(netnsDir, "owned-not-cni"))

	RemoveNetworkNamespaces(context.Background(), helper, owned)

	g.Expect(filepath.Join(netnsDir, "cni-owned")).ToNot(BeAnExistingFile())
	g.Expect(filepath.Join(netnsDir, "cni-foreign")).To(BeAnExistingFile())
	g.Expect(filepath.Join(netnsDir, "owned-not-cni")).To(BeAnExistingFile())
}
