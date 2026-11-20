package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	. "github.com/onsi/gomega"
)

func skipIfNoSystemctl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available")
	}
}

func TestServiceArgsFromMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]*string
		expected struct {
			updateArgs map[string]string
			deleteArgs []string
		}
	}{
		{
			name:  "NilValue",
			input: map[string]*string{"arg1": nil},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{},
				deleteArgs: []string{"arg1"},
			},
		},
		{
			name:  "EmptyString", // Should be threated as normal string
			input: map[string]*string{"arg1": Pointer("")},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{"arg1": ""},
				deleteArgs: []string{},
			},
		},
		{
			name:  "NonEmptyString",
			input: map[string]*string{"arg1": Pointer("value1")},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{"arg1": "value1"},
				deleteArgs: []string{},
			},
		},
		{
			name: "MixedValues",
			input: map[string]*string{
				"arg1": Pointer("value1"),
				"arg2": Pointer(""),
				"arg3": nil,
			},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{"arg1": "value1", "arg2": ""},
				deleteArgs: []string{"arg3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			updateArgs, deleteArgs := ServiceArgsFromMap(tt.input)
			g.Expect(updateArgs).To(Equal(tt.expected.updateArgs))
			g.Expect(deleteArgs).To(Equal(tt.expected.deleteArgs))
		})
	}
}

func TestGetUnitLoadState(t *testing.T) {
	skipIfNoSystemctl(t)
	ctx := context.Background()

	t.Run("NotFound", func(t *testing.T) {
		g := NewWithT(t)
		state, err := getUnitLoadState(ctx, "snap.k8s.definitely-nonexistent-k8sd-test.service")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(state).To(Equal(LoadState(LoadStateNotFound)))
	})

	t.Run("Loaded", func(t *testing.T) {
		// init.scope is always loaded and active on any systemd system.
		g := NewWithT(t)
		state, err := getUnitLoadState(ctx, "init.scope")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(state).To(Equal(LoadState(LoadStateLoaded)))
	})
}

func TestGetUnitActiveState(t *testing.T) {
	skipIfNoSystemctl(t)
	ctx := context.Background()

	t.Run("Active", func(t *testing.T) {
		// init.scope is always loaded and active on any systemd system.
		g := NewWithT(t)
		state, err := getUnitActiveState(ctx, "init.scope")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(state).To(Equal(ActiveStateActive))
	})

	t.Run("Inactive", func(t *testing.T) {
		// A loaded-but-inactive unit: systemd ships plymouth-quit.service which
		// is loaded but finishes quickly and stays inactive afterward.
		// Use a unit that is guaranteed to be loaded but not running:
		// systemd-ask-password-console.service is loaded but inactive on headless systems.
		// Fall back to checking any not-found unit — systemctl returns "inactive" for those too.
		g := NewWithT(t)
		state, err := getUnitActiveState(ctx, "snap.k8s.definitely-nonexistent-k8sd-test.service")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(state).To(Equal(ActiveStateInactive))
	})
}

func TestGetUnitMainPID(t *testing.T) {
	skipIfNoSystemctl(t)
	ctx := context.Background()

	t.Run("ActiveUnitHasNonZeroPID", func(t *testing.T) {
		// systemd-journald is always running on any systemd system.
		g := NewWithT(t)
		pid, err := getUnitMainPID(ctx, "systemd-journald.service")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pid).To(BeNumerically(">", 0))
	})

	t.Run("InactiveUnitHasZeroPID", func(t *testing.T) {
		g := NewWithT(t)
		pid, err := getUnitMainPID(ctx, "snap.k8s.definitely-nonexistent-k8sd-test.service")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pid).To(Equal(0))
	})
}

func TestRunningServiceArgs(t *testing.T) {
	skipIfNoSystemctl(t)
	ctx := context.Background()

	t.Run("UnitNotFound", func(t *testing.T) {
		// A completely unknown service name: LoadState=not-found.
		// This is NOT wrapped in ErrUnitNotRunning — it's a different error class.
		g := NewWithT(t)
		_, err := RunningServiceArgs(ctx, "definitely-nonexistent-k8sd-test-unit")
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, ErrUnitNotRunning)).To(BeFalse())
		g.Expect(err.Error()).To(ContainSubstring("was not found"))
	})
}

func createMockProcessWithExe(t *testing.T, baseDir string, pid int, comm string, state byte, exeTarget string, cmdlineParts []string) {
	t.Helper()
	pidDir := filepath.Join(baseDir, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}

	statContent := fmt.Sprintf("%d (%s) %c 1 2 3\n", pid, comm, state)
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(statContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var cmdlineBuf bytes.Buffer
	for _, part := range cmdlineParts {
		cmdlineBuf.WriteString(part)
		cmdlineBuf.WriteByte(0)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), cmdlineBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if exeTarget != "" {
		if err := os.Symlink(exeTarget, filepath.Join(pidDir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
}

func createMockProcess(t *testing.T, baseDir string, pid int, state byte, cmdlineParts []string) {
	t.Helper()
	createMockProcessWithExe(t, baseDir, pid, filepath.Base(cmdlineParts[0]), state, cmdlineParts[0], cmdlineParts)
}

func TestRunningServiceArgs_ProcFallback(t *testing.T) {
	oldProcDir := procDir
	oldUID := expectedServiceUID
	oldUsable := systemctlUsable.Load()
	t.Cleanup(func() {
		procDir = oldProcDir
		expectedServiceUID = oldUID
		systemctlUsable.Store(oldUsable)
	})

	t.Setenv("SNAP", "")
	t.Setenv("SNAP_INSTANCE_NAME", "")
	mockProc := t.TempDir()
	procDir = mockProc
	expectedServiceUID = uint32(os.Getuid())
	systemctlUsable.Store(false)
	ctx := context.Background()

	t.Run("ActiveService", func(t *testing.T) {
		g := NewWithT(t)
		createMockProcess(t, mockProc, 100, 'S', []string{
			"/snap/k8s/123/bin/kubelet",
			"--cluster-dns=10.152.183.10",
			"--port=10250",
		})

		args, err := RunningServiceArgs(ctx, "kubelet")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(Equal(map[string]string{
			"--cluster-dns": "10.152.183.10",
			"--port":        "10250",
		}))
	})

	t.Run("NonRunningService", func(t *testing.T) {
		g := NewWithT(t)
		_, err := RunningServiceArgs(ctx, "nonexistent-service")
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, ErrUnitNotRunning)).To(BeTrue())
	})

	t.Run("ZombieSkipped", func(t *testing.T) {
		g := NewWithT(t)
		createMockProcess(t, mockProc, 200, 'Z', []string{
			"/snap/k8s/123/bin/kube-proxy",
			"--config=/var/lib/kube-proxy/config.yaml",
		})

		_, err := RunningServiceArgs(ctx, "kube-proxy")
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, ErrUnitNotRunning)).To(BeTrue())
	})

	t.Run("ParallelInstance", func(t *testing.T) {
		g := NewWithT(t)
		createMockProcess(t, mockProc, 300, 'S', []string{
			"/snap/k8s_instance/x1/bin/kube-scheduler",
			"--leader-elect=true",
		})

		args, err := RunningServiceArgs(ctx, "kube-scheduler")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(Equal(map[string]string{
			"--leader-elect": "true",
		}))
	})

	t.Run("UnrelatedBinaryIgnored", func(t *testing.T) {
		g := NewWithT(t)
		createMockProcess(t, mockProc, 400, 'S', []string{
			"/usr/bin/containerd",
			"--config=/etc/containerd/config.toml",
		})
		createMockProcess(t, mockProc, 401, 'S', []string{
			"/snap/other/1/bin/containerd",
			"--config=/etc/containerd/config.toml",
		})

		_, err := RunningServiceArgs(ctx, "containerd")
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, ErrUnitNotRunning)).To(BeTrue())
	})

	t.Run("SpoofedArgv0OrWrongInstanceRejected", func(t *testing.T) {
		g := NewWithT(t)
		t.Setenv("SNAP_INSTANCE_NAME", "k8s_target")
		// Spoofed argv[0] pointing to k8s_target while actual /proc/<pid>/exe is /bin/sleep
		createMockProcessWithExe(t, mockProc, 50, "sleep", 'S', "/bin/sleep", []string{
			"/snap/k8s_target/10/bin/kube-apiserver",
			"--secure-port=9999",
		})
		// Real binary belonging to a sibling parallel instance k8s_other
		createMockProcessWithExe(t, mockProc, 51, "kube-apiserver", 'S', "/snap/k8s_other/10/bin/kube-apiserver", []string{
			"/snap/k8s_other/10/bin/kube-apiserver",
			"--secure-port=8888",
		})
		// Zombie with spaces and ')' in comm
		createMockProcessWithExe(t, mockProc, 52, "kube (api) server", 'Z', "/var/lib/snapd/snap/k8s_target/10/bin/kube-apiserver", []string{
			"/var/lib/snapd/snap/k8s_target/10/bin/kube-apiserver",
			"--secure-port=7777",
		})
		// Real target instance under /var/lib/snapd/snap/...
		createMockProcessWithExe(t, mockProc, 600, "kube-apiserver", 'S', "/var/lib/snapd/snap/k8s_target/10/bin/kube-apiserver", []string{
			"/var/lib/snapd/snap/k8s_target/10/bin/kube-apiserver",
			"--secure-port=6443",
		})

		args, err := RunningServiceArgs(ctx, "kube-apiserver")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(Equal(map[string]string{
			"--secure-port": "6443",
		}))
	})

	t.Run("StrictReadlinkPermissionDeniedUsesAppArmorProfile", func(t *testing.T) {
		g := NewWithT(t)
		strictProc := t.TempDir()
		oldProc := procDir
		oldReadlink := osReadlinkService
		procDir = strictProc
		osReadlinkService = func(string) (string, error) {
			return "", os.ErrPermission
		}
		t.Cleanup(func() {
			procDir = oldProc
			osReadlinkService = oldReadlink
		})
		t.Setenv("SNAP_INSTANCE_NAME", "k8s")

		// Pod process spoofing argv[0] under cri-containerd.apparmor.d must be rejected.
		createMockProcessWithExe(t, strictProc, 700, "kubelet", 'S', "", []string{
			"/snap/k8s/x1/bin/kubelet",
			"--cluster-dns=1.2.3.4",
		})
		g.Expect(os.MkdirAll(filepath.Join(strictProc, "700", "attr"), 0o755)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(strictProc, "700", "attr", "current"), []byte("cri-containerd.apparmor.d (enforce)\n"), 0o644)).To(Succeed())

		// Real confined kubelet under snap.k8s.kubelet must be matched.
		createMockProcessWithExe(t, strictProc, 701, "kubelet", 'S', "", []string{
			"/snap/k8s/x1/bin/kubelet",
			"--cluster-dns=10.152.183.51",
		})
		g.Expect(os.MkdirAll(filepath.Join(strictProc, "701", "attr"), 0o755)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(strictProc, "701", "attr", "current"), []byte(":lxd-node1:snap.k8s.kubelet (enforce)\n"), 0o644)).To(Succeed())

		args, err := RunningServiceArgs(ctx, "kubelet")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(Equal(map[string]string{
			"--cluster-dns": "10.152.183.51",
		}))
	})

	t.Run("StrictDetachedSnapfuseBinPathUsesAppArmorProfile", func(t *testing.T) {
		g := NewWithT(t)
		strictProc := t.TempDir()
		oldProc := procDir
		oldReadlink := osReadlinkService
		procDir = strictProc
		osReadlinkService = func(string) (string, error) {
			return "/bin/kubelet", nil
		}
		t.Cleanup(func() {
			procDir = oldProc
			osReadlinkService = oldReadlink
		})
		t.Setenv("SNAP_INSTANCE_NAME", "k8s")

		createMockProcessWithExe(t, strictProc, 702, "kubelet", 'S', "", []string{
			"/snap/k8s/x1/bin/kubelet",
			"--cluster-dns=10.152.183.99",
		})
		g.Expect(os.MkdirAll(filepath.Join(strictProc, "702", "attr"), 0o755)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(strictProc, "702", "attr", "current"), []byte("snap.k8s.kubelet (enforce)\n"), 0o644)).To(Succeed())

		args, err := RunningServiceArgs(ctx, "kubelet")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(Equal(map[string]string{
			"--cluster-dns": "10.152.183.99",
		}))
	})

	t.Run("KubernetesMulticallSymlinkExeMatched", func(t *testing.T) {
		g := NewWithT(t)
		multiProc := t.TempDir()
		oldProc := procDir
		oldReadlink := osReadlinkService
		procDir = multiProc
		osReadlinkService = func(string) (string, error) {
			return "/snap/k8s/x1/bin/kubernetes", nil
		}
		t.Cleanup(func() {
			procDir = oldProc
			osReadlinkService = oldReadlink
		})
		t.Setenv("SNAP_INSTANCE_NAME", "k8s")

		// kube-apiserver also points exe to bin/kubernetes, must not match kubelet lookup.
		createMockProcessWithExe(t, multiProc, 710, "kube-apiserver", 'S', "", []string{
			"/snap/k8s/x1/bin/kube-apiserver",
			"--secure-port=6443",
		})
		createMockProcessWithExe(t, multiProc, 711, "kubelet", 'S', "", []string{
			"/snap/k8s/x1/bin/kubelet",
			"--cluster-dns=10.152.183.77",
		})

		args, err := RunningServiceArgs(ctx, "kubelet")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(args).To(Equal(map[string]string{
			"--cluster-dns": "10.152.183.77",
		}))
	})
}
