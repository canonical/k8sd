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
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
)

func createFakeKubeProxy(t *testing.T, dir string, exitCode int, stderr string) {
	t.Helper()
	stderrFile := filepath.Join(dir, "stderr.txt")
	if err := os.WriteFile(stderrFile, []byte(stderr), 0o644); err != nil {
		t.Fatalf("failed to write stderr file: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncat %q >&2\nexit %d\n", stderrFile, exitCode)
	if err := os.WriteFile(filepath.Join(dir, "kube-proxy"), []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake kube-proxy script: %v", err)
	}
}

func TestRemoveKubeProxyRulesLogsStderrOnFailure(t *testing.T) {
	g := NewWithT(t)
	binDir := t.TempDir()
	createFakeKubeProxy(t, binDir, 1, "iptables v1.8.6 (legacy): Couldn't find target `KUBE-SERVICES'\n")
	s := &mock.Snap{Mock: mock.Mock{K8sBinDir: binDir}}

	logger := ktesting.NewLogger(t, ktesting.NewConfig(ktesting.BufferLogs(true)))
	internal.RemoveKubeProxyRules(klog.NewContext(context.Background(), logger), s)

	testingLogger, ok := logger.GetSink().(ktesting.Underlier)
	g.Expect(ok).To(BeTrue())
	logs := testingLogger.GetBuffer().String()
	g.Expect(logs).To(ContainSubstring("kube-proxy --cleanup reported errors"))
	g.Expect(logs).To(ContainSubstring("Couldn't find target `KUBE-SERVICES'"))
}
