package internal_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/snap/util/cleanup/internal"
	. "github.com/onsi/gomega"
)

func createFakeKubeProxy(t *testing.T, dir string, exitCode int, stdout, stderr string) {
	t.Helper()
	path := filepath.Join(dir, "kube-proxy")
	stdoutFile := filepath.Join(dir, "stdout.txt")
	stderrFile := filepath.Join(dir, "stderr.txt")
	if err := os.WriteFile(stdoutFile, []byte(stdout), 0o644); err != nil {
		t.Fatalf("failed to write stdout file: %v", err)
	}
	if err := os.WriteFile(stderrFile, []byte(stderr), 0o644); err != nil {
		t.Fatalf("failed to write stderr file: %v", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
cat %q
cat %q >&2
exit %d
`, stdoutFile, stderrFile, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake kube-proxy script: %v", err)
	}
}

func TestTryRemoveKubeProxyRules(t *testing.T) {
	ctx := context.Background()

	t.Run("Exit0_Success", func(t *testing.T) {
		g := NewWithT(t)
		binDir := t.TempDir()
		createFakeKubeProxy(t, binDir, 0, "", "")
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		err := internal.TryRemoveKubeProxyRules(ctx, s)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("ContainerIPVS_EPERM_Ignored", func(t *testing.T) {
		g := NewWithT(t)
		binDir := t.TempDir()
		stderr := `I1118 14:56:07.483606 2222854 server_linux.go:53] "Using iptables proxy"
E1118 14:56:07.518091 2222854 proxier.go:722] "Error flushing ipvs rules" err="operation not permitted"
E1118 14:56:07.580716 2222854 server.go:134] "Error running ProxyServer" err="encountered an error while tearing down rules"
E1118 14:56:07.580760 2222854 run.go:72] "command failed" err="encountered an error while tearing down rules"
`
		createFakeKubeProxy(t, binDir, 1, "", stderr)
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		err := internal.TryRemoveKubeProxyRules(ctx, s)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("ContainerIPVS_EPERM_WithIPTablesError_ReturnsError", func(t *testing.T) {
		g := NewWithT(t)
		binDir := t.TempDir()
		stderr := `I1118 14:56:07.483606 2222854 server_linux.go:53] "Using iptables proxy"
E1118 14:56:07.518091 2222854 proxier.go:722] "Error flushing ipvs rules" err="operation not permitted"
E1118 14:56:07.520000 2222854 iptables.go:100] "Error cleaning up iptables"
E1118 14:56:07.580716 2222854 server.go:134] "Error running ProxyServer" err="encountered an error while tearing down rules"
E1118 14:56:07.580760 2222854 run.go:72] "command failed" err="encountered an error while tearing down rules"
`
		createFakeKubeProxy(t, binDir, 1, "", stderr)
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		err := internal.TryRemoveKubeProxyRules(ctx, s)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("kube-proxy --cleanup failed"))
		g.Expect(err.Error()).To(ContainSubstring("Error cleaning up iptables"))
	})

	t.Run("NonKlogStderr_UnknownFlag_ReturnsError", func(t *testing.T) {
		g := NewWithT(t)
		binDir := t.TempDir()
		stderr := "unknown flag: --cleanup\n"
		createFakeKubeProxy(t, binDir, 1, "", stderr)
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		err := internal.TryRemoveKubeProxyRules(ctx, s)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("kube-proxy --cleanup failed"))
		g.Expect(err.Error()).To(ContainSubstring("unknown flag: --cleanup"))
	})

	t.Run("NonKlogStderr_IPTablesPermissionDenied_ReturnsError", func(t *testing.T) {
		g := NewWithT(t)
		binDir := t.TempDir()
		stderr := "iptables: Permission denied\n"
		createFakeKubeProxy(t, binDir, 1, "", stderr)
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		err := internal.TryRemoveKubeProxyRules(ctx, s)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("kube-proxy --cleanup failed"))
		g.Expect(err.Error()).To(ContainSubstring("iptables: Permission denied"))
	})

	t.Run("NonKlogStderr_NonKlogIPVSError_ReturnsError", func(t *testing.T) {
		g := NewWithT(t)
		binDir := t.TempDir()
		stderr := "Error flushing ipvs rules: operation not permitted\n"
		createFakeKubeProxy(t, binDir, 1, "", stderr)
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		err := internal.TryRemoveKubeProxyRules(ctx, s)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("kube-proxy --cleanup failed"))
		g.Expect(err.Error()).To(ContainSubstring("Error flushing ipvs rules: operation not permitted"))
	})

	t.Run("RemoveKubeProxyRules_NoErrorLogsOnIgnoredIPVS", func(t *testing.T) {
		binDir := t.TempDir()
		stderr := `I1118 14:56:07.483606 2222854 server_linux.go:53] "Using iptables proxy"
E1118 14:56:07.518091 2222854 proxier.go:722] "Error flushing ipvs rules" err="operation not permitted"
E1118 14:56:07.580716 2222854 server.go:134] "Error running ProxyServer" err="encountered an error while tearing down rules"
E1118 14:56:07.580760 2222854 run.go:72] "command failed" err="encountered an error while tearing down rules"
`
		createFakeKubeProxy(t, binDir, 1, "", stderr)
		s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

		// Should not panic or fail
		internal.RemoveKubeProxyRules(ctx, s)
	})
}

func TestIsIgnorableIPVSContainerCleanupError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		expected bool
	}{
		{
			name:     "Empty",
			stderr:   "",
			expected: false,
		},
		{
			name:     "WhitespaceOnly",
			stderr:   "   \n\t  \n",
			expected: false,
		},
		{
			name:     "InfoLogsOnly",
			stderr:   "I1118 14:56:07.483606 2222854 server_linux.go:53] \"Using iptables proxy\"\n",
			expected: false,
		},
		{
			name: "WarningAndInfoLogsOnly",
			stderr: "I1118 14:56:07.483606 2222854 server_linux.go:53] \"Using iptables proxy\"\n" +
				"W1118 14:56:07.483606 2222854 server_linux.go:60] \"Some warning\"\n",
			expected: false,
		},
		{
			name: "SummaryLinesOnlyNoIPVSError",
			stderr: "E1118 14:56:07.580716 2222854 server.go:134] \"Error running ProxyServer\" err=\"encountered an error while tearing down rules\"\n" +
				"E1118 14:56:07.580760 2222854 run.go:72] \"command failed\" err=\"encountered an error while tearing down rules\"\n",
			expected: false,
		},
		{
			name: "ExactContainerIPVSError",
			stderr: "I1118 14:56:07.483606 2222854 server_linux.go:53] \"Using iptables proxy\"\n" +
				"E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"operation not permitted\"\n" +
				"E1118 14:56:07.580716 2222854 server.go:134] \"Error running ProxyServer\" err=\"encountered an error while tearing down rules\"\n" +
				"E1118 14:56:07.580760 2222854 run.go:72] \"command failed\" err=\"encountered an error while tearing down rules\"\n",
			expected: true,
		},
		{
			name: "IPVSErrorWithoutSummaryLines",
			stderr: "I1118 14:56:07.483606 2222854 server_linux.go:53] \"Using iptables proxy\"\n" +
				"E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"operation not permitted\"\n",
			expected: true,
		},
		{
			name: "CaseInsensitiveOperationNotPermitted",
			stderr: "E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"Operation Not Permitted\"\n" +
				"E1118 14:56:07.580760 2222854 run.go:72] \"command failed\" err=\"encountered an error while tearing down rules\"\n",
			expected: true,
		},
		{
			name: "IPVSErrorWithIPTablesError",
			stderr: "E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"operation not permitted\"\n" +
				"E1118 14:56:07.520000 2222854 iptables.go:100] \"Error cleaning up iptables\"\n" +
				"E1118 14:56:07.580716 2222854 server.go:134] \"Error running ProxyServer\" err=\"encountered an error while tearing down rules\"\n",
			expected: false,
		},
		{
			name: "IPVSErrorWithNFTablesError",
			stderr: "E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"operation not permitted\"\n" +
				"E1118 14:56:07.520000 2222854 nftables.go:88] \"failed to delete table: permission denied\"\n",
			expected: false,
		},
		{
			name: "IPVSErrorWithNonKlogError",
			stderr: "E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"operation not permitted\"\n" +
				"iptables: Resource temporarily unavailable\n",
			expected: false,
		},
		{
			name: "PanicTraceback",
			stderr: "E1118 14:56:07.518091 2222854 proxier.go:722] \"Error flushing ipvs rules\" err=\"operation not permitted\"\n" +
				"panic: runtime error: invalid memory address or nil pointer dereference\n" +
				"[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x12345]\n",
			expected: false,
		},
		{
			name:     "NonKlogIPVSErrorLineFailsClosed",
			stderr:   "Error flushing ipvs rules: operation not permitted\n",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			actual := internal.IsIgnorableIPVSContainerCleanupError(tc.stderr)
			g.Expect(actual).To(Equal(tc.expected))
		})
	}
}
