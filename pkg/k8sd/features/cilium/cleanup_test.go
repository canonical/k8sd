package cilium_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/features/cilium"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

func createFakeExe(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to create fake exe %s: %v", name, err)
	}
	return path
}

func setupTestPath(t *testing.T, tmpDir string) {
	t.Helper()
	t.Setenv("PATH", tmpDir)
}

func TestCleanupNetwork_Success(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	cniDir := filepath.Join(tmpDir, "cni")
	g.Expect(os.MkdirAll(cniDir, 0o755)).To(Succeed())

	ciliumCallsLog := filepath.Join(tmpDir, "cilium-dbg.log")
	createFakeExe(t, cniDir, "cilium-dbg", `#!/bin/sh
echo "$@" >> `+ciliumCallsLog+`
exit 0
`)

	iptablesRestoreLog := filepath.Join(tmpDir, "iptables-restore.log")
	createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:CILIUM_INPUT - [0:0]
-A INPUT -j CILIUM_INPUT
-A INPUT -m comment --comment "keep-this-rule" -j ACCEPT
COMMIT
EOF
`)
	createFakeExe(t, tmpDir, "iptables-restore", `#!/bin/sh
/bin/cat - >> `+iptablesRestoreLog+`
exit 0
`)

	// ip6tables has no cilium rules; ip6tables-restore must not be called.
	ip6tablesRestoreLog := filepath.Join(tmpDir, "ip6tables-restore.log")
	createFakeExe(t, tmpDir, "ip6tables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:INPUT ACCEPT [0:0]
-A INPUT -m comment --comment "ipv6-keep" -j ACCEPT
COMMIT
EOF
`)
	createFakeExe(t, tmpDir, "ip6tables-restore", `#!/bin/sh
/bin/cat - >> `+ip6tablesRestoreLog+`
exit 0
`)

	// iptables-legacy and ip6tables-legacy are absent (exec.ErrNotFound)
	setupTestPath(t, tmpDir)

	s := &snapmock.Snap{
		Mock: snapmock.Mock{
			CNIBinDir: cniDir,
		},
	}

	err := cilium.CleanupNetwork(context.Background(), s)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify cilium-dbg was invoked with expected flags from snap.CNIBinDir()
	calls, err := os.ReadFile(ciliumCallsLog)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(strings.TrimSpace(string(calls))).To(Equal("post-uninstall-cleanup --all-state --force"))

	// Verify iptables rules were filtered and restored
	restored, err := os.ReadFile(iptablesRestoreLog)
	g.Expect(err).NotTo(HaveOccurred())
	restoredStr := string(restored)
	g.Expect(restoredStr).To(ContainSubstring("keep-this-rule"))
	g.Expect(restoredStr).To(ContainSubstring("*filter"))
	g.Expect(restoredStr).To(ContainSubstring("COMMIT"))
	g.Expect(strings.ToLower(restoredStr)).NotTo(ContainSubstring("cilium"))

	// Verify ip6tables-restore was never called because no cilium rules were present
	_, err = os.Stat(ip6tablesRestoreLog)
	g.Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
}

func TestCleanupNetwork_CiliumDbgFails_ContinuesIptables(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	cniDir := filepath.Join(tmpDir, "cni")
	g.Expect(os.MkdirAll(cniDir, 0o755)).To(Succeed())

	createFakeExe(t, cniDir, "cilium-dbg", `#!/bin/sh
echo "Failed to remove link cilium_host" >&2
exit 1
`)

	iptablesRestoreLog := filepath.Join(tmpDir, "iptables-restore.log")
	createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:CILIUM_INPUT - [0:0]
-A INPUT -j CILIUM_INPUT
-A INPUT -j ACCEPT
COMMIT
EOF
`)
	createFakeExe(t, tmpDir, "iptables-restore", `#!/bin/sh
/bin/cat - >> `+iptablesRestoreLog+`
exit 0
`)

	setupTestPath(t, tmpDir)

	s := &snapmock.Snap{
		Mock: snapmock.Mock{
			CNIBinDir: cniDir,
		},
	}

	err := cilium.CleanupNetwork(context.Background(), s)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cilium-dbg cleanup failed"))
	g.Expect(err.Error()).To(ContainSubstring("Failed to remove link cilium_host"))

	// iptables rules must still be cleaned despite cilium-dbg failure
	restored, err := os.ReadFile(iptablesRestoreLog)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(restored)).To(ContainSubstring("-A INPUT -j ACCEPT"))
	g.Expect(strings.ToLower(string(restored))).NotTo(ContainSubstring("cilium"))
}

func TestCleanupNetwork_ContinuingAcrossVariantsAndJoiningErrors(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	// iptables-save fails with error
	createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
echo "iptables-save error" >&2
exit 1
`)

	// ip6tables-save succeeds and has cilium rules; ip6tables-restore succeeds
	ip6RestoreLog := filepath.Join(tmpDir, "ip6tables-restore.log")
	createFakeExe(t, tmpDir, "ip6tables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:CILIUM_FORWARD - [0:0]
-A FORWARD -j CILIUM_FORWARD
-A FORWARD -j ACCEPT
COMMIT
EOF
`)
	createFakeExe(t, tmpDir, "ip6tables-restore", `#!/bin/sh
/bin/cat - >> `+ip6RestoreLog+`
exit 0
`)

	// iptables-legacy-save is missing (exec.ErrNotFound -> skipped cleanly)

	// ip6tables-legacy-save has cilium rules, but ip6tables-legacy-restore fails
	createFakeExe(t, tmpDir, "ip6tables-legacy-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
-A INPUT -j CILIUM_LEGACY
COMMIT
EOF
`)
	createFakeExe(t, tmpDir, "ip6tables-legacy-restore", `#!/bin/sh
echo "restore failed" >&2
exit 1
`)

	setupTestPath(t, tmpDir)

	err := cilium.CleanupNetwork(context.Background(), nil)
	g.Expect(err).To(HaveOccurred())

	errStr := err.Error()
	g.Expect(errStr).To(ContainSubstring("failed to read iptables rules"))
	g.Expect(errStr).To(ContainSubstring("failed to restore ip6tables-legacy rules"))
	g.Expect(errStr).NotTo(ContainSubstring("iptables-legacy-save"))

	// Verify ip6tables was cleaned despite iptables failing earlier
	restored, err := os.ReadFile(ip6RestoreLog)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(restored)).To(ContainSubstring("-A FORWARD -j ACCEPT"))
	g.Expect(strings.ToLower(string(restored))).NotTo(ContainSubstring("cilium"))
}

func TestCleanupNetwork_MissingRestoreBinarySkipped(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	// iptables-save outputs cilium rules, but iptables-restore is not installed
	createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:CILIUM_INPUT - [0:0]
COMMIT
EOF
`)
	// ip6tables-save and ip6tables-restore succeed
	ip6RestoreLog := filepath.Join(tmpDir, "ip6tables-restore.log")
	createFakeExe(t, tmpDir, "ip6tables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:CILIUM_INPUT - [0:0]
-A INPUT -j ACCEPT
COMMIT
EOF
`)
	createFakeExe(t, tmpDir, "ip6tables-restore", `#!/bin/sh
/bin/cat - >> `+ip6RestoreLog+`
exit 0
`)

	setupTestPath(t, tmpDir)

	err := cilium.CleanupNetwork(context.Background(), nil)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify ip6tables-restore ran and cleaned rules
	restored, err := os.ReadFile(ip6RestoreLog)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(restored)).To(ContainSubstring("-A INPUT -j ACCEPT"))
	g.Expect(strings.ToLower(string(restored))).NotTo(ContainSubstring("cilium"))
}

func TestCleanupNetwork_ContextCancelled(t *testing.T) {
	t.Run("PreCancelled", func(t *testing.T) {
		g := NewWithT(t)

		tmpDir := t.TempDir()
		callsLog := filepath.Join(tmpDir, "calls.log")
		createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
echo "called" >> `+callsLog+`
exit 0
`)
		setupTestPath(t, tmpDir)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := cilium.CleanupNetwork(ctx, nil)
		g.Expect(err).To(MatchError(context.Canceled))

		_, err = os.Stat(callsLog)
		g.Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	t.Run("TimeoutDuringCiliumDbg", func(t *testing.T) {
		g := NewWithT(t)

		tmpDir := t.TempDir()
		cniDir := filepath.Join(tmpDir, "cni")
		g.Expect(os.MkdirAll(cniDir, 0o755)).To(Succeed())

		// cilium-dbg sleeps longer than timeout
		createFakeExe(t, cniDir, "cilium-dbg", `#!/bin/sh
exec /bin/sleep 5
`)
		iptablesCallsLog := filepath.Join(tmpDir, "iptables.log")
		createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
echo "iptables-called" >> `+iptablesCallsLog+`
exit 0
`)
		setupTestPath(t, tmpDir)

		s := &snapmock.Snap{
			Mock: snapmock.Mock{
				CNIBinDir: cniDir,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := cilium.CleanupNetwork(ctx, s)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

		// Subsequent iptables command must not have run
		_, err = os.Stat(iptablesCallsLog)
		g.Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	t.Run("TimeoutDuringIptablesSave", func(t *testing.T) {
		g := NewWithT(t)

		tmpDir := t.TempDir()
		createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
exec /bin/sleep 5
`)
		ip6tablesCallsLog := filepath.Join(tmpDir, "ip6tables.log")
		createFakeExe(t, tmpDir, "ip6tables-save", `#!/bin/sh
echo "ip6tables-called" >> `+ip6tablesCallsLog+`
exit 0
`)
		setupTestPath(t, tmpDir)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := cilium.CleanupNetwork(ctx, nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

		// Subsequent ip6tables command must not have run
		_, err = os.Stat(ip6tablesCallsLog)
		g.Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	t.Run("TimeoutDuringIptablesRestore", func(t *testing.T) {
		g := NewWithT(t)

		tmpDir := t.TempDir()
		createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
/bin/cat << 'EOF'
*filter
:CILIUM_INPUT - [0:0]
COMMIT
EOF
`)
		createFakeExe(t, tmpDir, "iptables-restore", `#!/bin/sh
exec /bin/sleep 5
`)
		ip6tablesCallsLog := filepath.Join(tmpDir, "ip6tables.log")
		createFakeExe(t, tmpDir, "ip6tables-save", `#!/bin/sh
echo "ip6tables-called" >> `+ip6tablesCallsLog+`
exit 0
`)
		setupTestPath(t, tmpDir)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := cilium.CleanupNetwork(ctx, nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

		// Subsequent ip6tables command must not have run
		_, err = os.Stat(ip6tablesCallsLog)
		g.Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	t.Run("StrictUsesBpfStateFlag", func(t *testing.T) {
		g := NewWithT(t)

		tmpDir := t.TempDir()
		cniDir := filepath.Join(tmpDir, "cni")
		g.Expect(os.MkdirAll(cniDir, 0o755)).To(Succeed())

		ciliumCallsLog := filepath.Join(tmpDir, "cilium-dbg.log")
		createFakeExe(t, cniDir, "cilium-dbg", `#!/bin/sh
echo "$@" >> `+ciliumCallsLog+`
exit 0
`)
		createFakeExe(t, tmpDir, "iptables-save", `#!/bin/sh
exit 0
`)
		createFakeExe(t, tmpDir, "ip6tables-save", `#!/bin/sh
exit 0
`)
		setupTestPath(t, tmpDir)

		s := &snapmock.Snap{
			Mock: snapmock.Mock{
				Strict:    true,
				CNIBinDir: cniDir,
			},
		}

		g.Expect(cilium.CleanupNetwork(context.Background(), s)).To(Succeed())
		calls, err := os.ReadFile(ciliumCallsLog)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(string(calls))).To(Equal("post-uninstall-cleanup --bpf-state --force"))
	})
}
