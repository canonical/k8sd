package cilium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/snap"
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
	procDir := filepath.Join(tmpDir, "proc")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("failed to create fake proc dir: %v", err)
	}
	oldPIDFile := defaultCiliumPIDFile
	oldDirs := defaultCiliumStateDirs
	oldProcDir := defaultProcDir
	oldQuiesceServices := defaultQuiesceRuntimeServices
	defaultCiliumPIDFile = filepath.Join(tmpDir, "cilium.pid")
	defaultCiliumStateDirs = []string{filepath.Join(tmpDir, "run-cilium"), filepath.Join(tmpDir, "var-lib-cilium")}
	defaultProcDir = procDir
	defaultQuiesceRuntimeServices = func(ctx context.Context, s snap.Snap) (func() error, error) {
		if s != nil {
			return quiesceSnapRuntimeServices(ctx, s)
		}
		return func() error { return nil }, nil
	}
	t.Cleanup(func() {
		defaultCiliumPIDFile = oldPIDFile
		defaultCiliumStateDirs = oldDirs
		defaultProcDir = oldProcDir
		defaultQuiesceRuntimeServices = oldQuiesceServices
	})
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

	err := CleanupNetwork(context.Background(), s)
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

	err := CleanupNetwork(context.Background(), s)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cilium-dbg cleanup failed"))

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

	err := CleanupNetwork(context.Background(), nil)
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

	err := CleanupNetwork(context.Background(), nil)
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

		err := CleanupNetwork(ctx, nil)
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

		err := CleanupNetwork(ctx, s)
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

		err := CleanupNetwork(ctx, nil)
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

		err := CleanupNetwork(ctx, nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

		// Subsequent ip6tables command must not have run
		_, err = os.Stat(ip6tablesCallsLog)
		g.Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})
}

func TestCleanupNetworkAttemptsEveryFirewallAfterFailures(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			dir := t.TempDir()
			record := filepath.Join(dir, "commands")
			setupTestPath(t, dir)
			t.Setenv("CLEANUP_TEST_RECORD", record)
			for _, name := range []string{"iptables", "ip6tables", "iptables-legacy", "ip6tables-legacy"} {
				save := "#!/bin/sh\nprintf '%s\\n' '$NAME-save' >> \"$CLEANUP_TEST_RECORD\"\nprintf '%s\\n' '-A KEEP -j ACCEPT' '-A CILIUM_TEST -j ACCEPT'\n"
				save = strings.ReplaceAll(save, "$NAME", name)
				if fail && name == "iptables" {
					save += "exit 19\n"
				}
				restore := "#!/bin/sh\nprintf '%s\\n' '$NAME-restore' >> \"$CLEANUP_TEST_RECORD\"\nwhile IFS= read -r line; do printf '%s\\n' \"$line\" >> \"$CLEANUP_TEST_RECORD\"; done\n"
				restore = strings.ReplaceAll(restore, "$NAME", name)
				if fail && name == "ip6tables" {
					restore += "exit 20\n"
				}
				if err := os.WriteFile(filepath.Join(dir, name+"-save"), []byte(save), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name+"-restore"), []byte(restore), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if fail {
				if err := os.WriteFile(filepath.Join(dir, "cilium-cni"), nil, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			s := &snapmock.Snap{Mock: snapmock.Mock{Strict: true, CNIBinDir: dir}}
			err := CleanupNetwork(context.Background(), s)
			if (err != nil) != fail {
				t.Fatalf("cleanup failed=%t err=%v", fail, err)
			}
			if fail {
				for _, message := range []string{"helper is missing", "read iptables", "restore ip6tables"} {
					if !strings.Contains(err.Error(), message) {
						t.Fatalf("missing error %q: %v", message, err)
					}
				}
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "ip6tables-legacy-restore") || !strings.Contains(string(data), "-A KEEP") || strings.Contains(string(data), "-A CILIUM_TEST") {
				t.Fatalf("incorrect firewall cleanup: %s", data)
			}
		})
	}
}

func TestCleanupHelperRejectsInvalidPathsBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	setupTestPath(t, dir)
	s := &snapmock.Snap{Mock: snapmock.Mock{Strict: true, CNIBinDir: dir}}
	helper := filepath.Join(dir, "cilium-dbg")
	if err := os.Symlink(filepath.Join(dir, "missing"), helper); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCiliumState(context.Background(), s); err == nil {
		t.Fatal("symlink helper accepted")
	}
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s = &snapmock.Snap{Mock: snapmock.Mock{Strict: true, CNIBinDir: file}}
	if err := cleanupCiliumState(context.Background(), s); err == nil {
		t.Fatal("invalid helper directory accepted")
	}
}

func TestCleanupHelperDoesNotRunWhenPIDFileCannotBeRemoved(t *testing.T) {
	dir := t.TempDir()
	setupTestPath(t, dir)
	pid := filepath.Join(dir, "pid")
	if err := os.Mkdir(pid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pid, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runCiliumCleanup(context.Background(), filepath.Join(dir, "must-not-run"), pid); err == nil || !strings.Contains(err.Error(), "PID file") {
		t.Fatalf("PID removal failure lost: %v", err)
	}
}

func TestCleanupHelperCancellationIsBounded(t *testing.T) {
	dir := t.TempDir()
	setupTestPath(t, dir)
	helper := filepath.Join(dir, "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := runCiliumCleanup(ctx, helper, filepath.Join(dir, "pid")); err == nil {
		t.Fatal("cancellation did not fail cleanup")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cleanup cancellation did not stop its child")
	}
}

func TestMissingStrictCleanupHelperIsReported(t *testing.T) {
	dir := t.TempDir()
	setupTestPath(t, dir)
	s := &snapmock.Snap{Mock: snapmock.Mock{Strict: true, CNIBinDir: dir}}
	if err := cleanupCiliumState(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cilium-cni"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCiliumState(context.Background(), s); err == nil || !strings.Contains(err.Error(), "helper is missing") {
		t.Fatalf("missing helper not reported: %v", err)
	}
}

type cleanupFileInfo struct {
	os.FileInfo
	uid uint32
}

func (i cleanupFileInfo) Sys() any { return &syscall.Stat_t{Uid: i.uid} }

func TestCleanupHelperRejectsUntrustedExecutables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCleanupHelper(cleanupFileInfo{info, 0}); err != nil {
		t.Fatal(err)
	}
	untrustedUID := uint32(1000)
	if uint32(os.Geteuid()) == untrustedUID {
		untrustedUID = 1001
	}
	if err := validateCleanupHelper(cleanupFileInfo{info, untrustedUID}); err == nil {
		t.Fatal("accepted user-owned helper")
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Lstat(path)
	if err := validateCleanupHelper(cleanupFileInfo{info, 0}); err == nil {
		t.Fatal("accepted writable helper")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Lstat(link)
	if err := validateCleanupHelper(cleanupFileInfo{info, 0}); err == nil {
		t.Fatal("accepted symlink helper")
	}
}

func TestCleanupHelperUsesOwnedPathAndReportsFailure(t *testing.T) {
	dir := t.TempDir()
	setupTestPath(t, dir)
	helper := filepath.Join(dir, "helper")
	pidFile := filepath.Join(dir, "cilium.pid")
	for _, status := range []string{"0", "17"} {
		script := "#!/bin/sh\n[ \"$*\" = 'post-uninstall-cleanup --all-state --force' ] || exit 90\nexit " + status + "\n"
		if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pidFile, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		err := runCiliumCleanup(context.Background(), helper, pidFile)
		if (err == nil) != (status == "0") {
			t.Fatalf("status %s: %v", status, err)
		}
		if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
			t.Fatalf("PID file retained: %v", err)
		}
	}
}

func TestCleanupStateDispatchAndErrorPreservation(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, trusted := range []bool{false, true} {
			t.Run(fmt.Sprintf("strict=%t/trusted=%t", strict, trusted), func(t *testing.T) {
				dir := t.TempDir()
				helper := filepath.Join(dir, "cilium-dbg")
				if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				statHelper := func(path string) (os.FileInfo, error) {
					if path != helper {
						t.Fatalf("unexpected helper path: %s", path)
					}
					info, err := os.Lstat(path)
					if err != nil {
						return nil, err
					}
					uid := uint32(0)
					if !trusted {
						uid = 1000
						if uint32(os.Geteuid()) == 1000 {
							uid = 1001
						}
					}
					return cleanupFileInfo{info, uid}, nil
				}
				calls := 0
				helperCalls := 0
				testPIDFile := filepath.Join(dir, "cilium.pid")
				helperErr := errors.New("helper cleanup failed")
				runCleanup := func(_ context.Context, actualHelper, pidFile string) error {
					helperCalls++
					if actualHelper != helper || pidFile != testPIDFile {
						t.Fatalf("unexpected cleanup paths: %s %s", actualHelper, pidFile)
					}
					return helperErr
				}
				directoryErr := errors.New("directory cleanup failed")
				cleanupDirectories := func(context.Context) error { calls++; return directoryErr }
				checkStopped := func(context.Context, string) error { return nil }
				s := &snapmock.Snap{Mock: snapmock.Mock{Strict: strict, CNIBinDir: dir}}
				err := cleanupCiliumStateWith(context.Background(), s, testPIDFile, statHelper, checkStopped, runCleanup, cleanupDirectories)
				if !trusted {
					if helperCalls != 0 || err == nil || !strings.Contains(err.Error(), "root-owned") {
						t.Fatalf("unsafe helper ran cleanup: helperCalls=%d err=%v", helperCalls, err)
					}
					if calls != 1 || !errors.Is(err, directoryErr) {
						t.Fatalf("directory cleanup failure lost: calls=%d err=%v", calls, err)
					}
					return
				}
				if helperCalls != 1 || !errors.Is(err, helperErr) {
					t.Fatalf("helper failure lost: %v", err)
				}
				if calls != 1 || !errors.Is(err, directoryErr) {
					t.Fatalf("directory cleanup failure lost: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestCleanupNetworkRefusesWhileCiliumAgentRunning(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestPath(t, tmpDir)

	cniDir := filepath.Join(tmpDir, "cni")
	if err := os.MkdirAll(cniDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperCalled := filepath.Join(tmpDir, "helper-called")
	createFakeExe(t, cniDir, "cilium-dbg", "#!/bin/sh\nset -e\n: > "+helperCalled+"\n")
	iptablesCalled := filepath.Join(tmpDir, "iptables-called")
	createFakeExe(t, tmpDir, "iptables-save", "#!/bin/sh\nset -e\n: > "+iptablesCalled+"\n")
	stateFile := filepath.Join(defaultCiliumStateDirs[0], "live.sock")
	if err := os.MkdirAll(defaultCiliumStateDirs[0], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}

	agentProc := filepath.Join(defaultProcDir, "4242")
	if err := os.MkdirAll(agentProc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentProc, "comm"), []byte("cilium-agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentProc, "stat"), []byte("4242 (cilium-agent) S 1 4242 4242 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &snapmock.Snap{Mock: snapmock.Mock{CNIBinDir: cniDir}}
	err := CleanupNetwork(context.Background(), s)
	if !errors.Is(err, errCiliumAgentRunning) {
		t.Fatalf("expected running cilium-agent refusal, got: %v", err)
	}
	// Nil snap with no CNI dir must also refuse before touching iptables.
	if err := CleanupNetwork(context.Background(), nil); !errors.Is(err, errCiliumAgentRunning) {
		t.Fatalf("expected running cilium-agent refusal for nil snap, got: %v", err)
	}
	// Unreadable procDir must also fail closed before touching iptables.
	badProc := filepath.Join(tmpDir, "not-a-proc-dir")
	if err := os.WriteFile(badProc, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	savedProc := defaultProcDir
	defaultProcDir = badProc
	if err := CleanupNetwork(context.Background(), nil); err == nil {
		t.Fatal("expected unreadable procDir to fail closed")
	}
	defaultProcDir = savedProc

	if _, statErr := os.Stat(helperCalled); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("cilium-dbg ran while cilium-agent was running")
	}
	if _, statErr := os.Stat(iptablesCalled); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("iptables ran while cilium-agent was running")
	}
	if _, statErr := os.Stat(stateFile); statErr != nil {
		t.Fatalf("live Cilium state was deleted: %v", statErr)
	}

	// Once cilium-agent transitions to zombie/exited, cleanup proceeds (including for nil snap).
	if err := os.WriteFile(filepath.Join(agentProc, "stat"), []byte("4242 (cilium-agent) Z 1 4242 4242 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(defaultCiliumStateDirs[1], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CleanupNetwork(context.Background(), nil); err != nil {
		t.Fatalf("expected nil-snap cleanup to succeed after agent exit, got: %v", err)
	}
	if _, statErr := os.Lstat(defaultCiliumStateDirs[0]); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected residual state dir[0] to be removed for nil snap, got: %v", statErr)
	}
	if _, statErr := os.Lstat(defaultCiliumStateDirs[1]); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected residual state dir[1] to be removed for nil snap, got: %v", statErr)
	}

	if err := CleanupNetwork(context.Background(), s); err != nil {
		t.Fatalf("expected cleanup to succeed after agent exit, got: %v", err)
	}
	if _, statErr := os.Stat(helperCalled); statErr != nil {
		t.Fatalf("expected cilium-dbg to run after agent exit, got: %v", statErr)
	}
}

func TestCleanupNetworkQuiescesAndRestoresActiveRuntimeServices(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestPath(t, tmpDir)
	t.Setenv("SNAP_CONTEXT", "")

	stopErr := errors.New("snapd stop failed")
	s := &snapmock.Snap{
		ActiveServicesResult: []string{"containerd", "kubelet"},
		StopServicesErr:      stopErr,
		Mock: snapmock.Mock{
			LockFilesDir: filepath.Join(tmpDir, "locks"),
		},
	}
	iptablesCalled := filepath.Join(tmpDir, "iptables-called")
	createFakeExe(t, tmpDir, "iptables-save", "#!/bin/sh\nset -e\n: > "+iptablesCalled+"\n")

	err := CleanupNetwork(context.Background(), s)
	if !errors.Is(err, stopErr) {
		t.Fatalf("expected StopServices error, got: %v", err)
	}
	if len(s.StopServicesCalledWith) != 2 || len(s.StopServicesCalledWith[1]) != 1 || s.StopServicesCalledWith[1][0] != "--no-disable" {
		t.Fatalf("expected --no-disable passed to StopServices, got: %v", s.StopServicesCalledWith)
	}
	if len(s.StartServicesCalledWith) != 0 {
		t.Fatalf("unexpected StartServices after failed stop: %v", s.StartServicesCalledWith)
	}
	if _, statErr := os.Stat(iptablesCalled); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("iptables ran despite StopServices failure")
	}

	// When active services were stopped, standalone CleanupNetwork restarts only those with --no-enable.
	s2 := &snapmock.Snap{
		ActiveServicesResult: []string{"containerd", "kubelet"},
		Mock: snapmock.Mock{
			LockFilesDir: filepath.Join(tmpDir, "locks"),
		},
	}
	if err := CleanupNetwork(context.Background(), s2); err != nil {
		t.Fatalf("expected cleanup to succeed and restore services, got: %v", err)
	}
	if len(s2.StartServicesCalledWith) != 2 ||
		strings.Join(s2.StartServicesCalledWith[0], ",") != "containerd,kubelet" ||
		strings.Join(s2.StartServicesCalledWith[1], ",") != "--no-enable" {
		t.Fatalf("expected active services restarted with --no-enable, got: %v", s2.StartServicesCalledWith)
	}

	if _, err := quiesceSnapRuntimeServices(context.Background(), nil); err == nil {
		t.Fatal("expected nil snap outside snap hook to fail closed")
	}

	// Context cancellation while the in-process semaphore is held must return immediately.
	ciliumCleanupSem <- struct{}{}
	defer func() { <-ciliumCleanupSem }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := CleanupNetwork(ctx, &snapmock.Snap{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded while semaphore held, got: %v", err)
	}
}
