package snaputil_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/snap/mock"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestRemoveDeprecatedKubeletFlags_RemovesFlagAndRestarts(t *testing.T) {
	g := NewWithT(t)
	dir := t.TempDir()

	g.Expect(utils.WriteFile(filepath.Join(dir, "kubelet"), []byte(
		"--container-runtime-endpoint=\"/run/containerd/containerd.sock\"\n--containerd=\"\"\n",
	), 0o600)).To(Succeed())

	s := &mock.Snap{Mock: mock.Mock{ServiceArgumentsDir: dir}}

	g.Expect(snaputil.RemoveDeprecatedKubeletFlags(context.Background(), s)).To(Succeed())

	value, err := snaputil.GetServiceArgument(s, "kubelet", "--containerd")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(value).To(BeEmpty(), "deprecated flag should have been removed")

	keep, err := snaputil.GetServiceArgument(s, "kubelet", "--container-runtime-endpoint")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(keep).To(Equal("/run/containerd/containerd.sock"), "unrelated flags must be preserved")

	g.Expect(s.RestartServicesCalledWith).To(HaveLen(1), "kubelet should be restarted once the flag is removed")
}

func TestRemoveDeprecatedKubeletFlags_NoOpWhenFlagAbsent(t *testing.T) {
	g := NewWithT(t)
	dir := t.TempDir()

	g.Expect(utils.WriteFile(filepath.Join(dir, "kubelet"), []byte(
		"--container-runtime-endpoint=\"/run/containerd/containerd.sock\"\n",
	), 0o600)).To(Succeed())

	s := &mock.Snap{Mock: mock.Mock{ServiceArgumentsDir: dir}}

	g.Expect(snaputil.RemoveDeprecatedKubeletFlags(context.Background(), s)).To(Succeed())
	g.Expect(s.RestartServicesCalledWith).To(BeEmpty(), "kubelet should not be restarted when nothing changed")
}
