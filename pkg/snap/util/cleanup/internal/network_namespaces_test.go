package internal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/snap/util/cleanup/internal"
	netnsutils "github.com/canonical/k8sd/pkg/utils/netns"
	. "github.com/onsi/gomega"
)

func TestRemoveNetworkNamespaces(t *testing.T) {
	ctx := context.Background()
	netnsDir := t.TempDir()
	g := NewWithT(t)

	helper := netnsutils.NewMockNetworkNSHelper(netnsDir)

	// Create namespaces: one owned cni- and one other cni- namespace from another runtime
	cniNS := "cni-test-ns"
	otherNS := "cni-other-runtime"
	cniPath := filepath.Join(netnsDir, cniNS)
	otherPath := filepath.Join(netnsDir, otherNS)
	g.Expect(os.Mkdir(cniPath, 0o755)).To(Succeed())
	g.Expect(os.Mkdir(otherPath, 0o755)).To(Succeed())
	g.Expect(cniPath).To(BeAnExistingFile())
	g.Expect(otherPath).To(BeAnExistingFile())

	err := internal.RemoveNetworkNamespaces(ctx, helper, map[string]bool{cniNS: true})
	g.Expect(err).To(Succeed())

	// Only owned cni- namespace should be deleted, other should remain
	g.Expect(cniPath).ToNot(BeAnExistingFile())
	g.Expect(otherPath).To(BeAnExistingFile())
}

type failingNamespaceManager struct {
	deleteErr  error
	iterateErr error
	deleted    []string
}

func (m *failingNamespaceManager) ForEachNetworkNamespace(ctx context.Context, callback func(context.Context, string) error) error {
	for _, name := range []string{"cni-owned", "cni-unrelated"} {
		if err := callback(ctx, name); err != nil {
			return err
		}
	}
	return m.iterateErr
}

func (m *failingNamespaceManager) DeleteNetworkNamespace(_ context.Context, name string) error {
	m.deleted = append(m.deleted, name)
	return m.deleteErr
}

func TestRemoveNetworkNamespacesReportsAllFailures(t *testing.T) {
	removeErr, listErr := errors.New("delete denied"), errors.New("enumeration failed")
	manager := &failingNamespaceManager{deleteErr: removeErr, iterateErr: listErr}
	err := internal.RemoveNetworkNamespaces(context.Background(), manager, map[string]bool{"cni-owned": true})
	if !errors.Is(err, removeErr) || !errors.Is(err, listErr) {
		t.Fatalf("lost errors: %v", err)
	}
	if len(manager.deleted) != 1 || manager.deleted[0] != "cni-owned" {
		t.Fatalf("touched unrelated namespace: %v", manager.deleted)
	}
	if err := internal.RemoveNetworkNamespaces(context.Background(), manager, nil); err != nil {
		t.Fatalf("enumerated namespaces without ownership: %v", err)
	}
}
