package shims

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"golang.org/x/sys/unix"
)

func TestParseProcessListAndShimChildren(t *testing.T) {
	raw := `    1     0 /sbin/init
  200     1 /snap/k8s/1234/bin/containerd-shim-runc-v2 -namespace k8s.io -id abc
  201   200 /pause
  202   200 /usr/local/bin/coredns -conf /etc/coredns/Corefile
  203   202 /bin/helper --flag
  300     1 /usr/sbin/sshd -D
`

	procs := parseProcessList(raw)
	readlink := func(path string) (string, error) {
		if path == "/proc/200/exe" {
			return "/snap/k8s/1234/bin/containerd-shim-runc-v2", nil
		}
		return "", os.ErrNotExist
	}
	pids := findAllChildren(findShimPIDsWithReadlink(procs, readlink), makeShallowChildPIDs(procs))

	for _, want := range []string{"200", "201", "202", "203"} {
		if !slices.Contains(pids, want) {
			t.Errorf("expected PID %s in shim descendants, got %v", want, pids)
		}
	}
	for _, unwanted := range []string{"1", "300"} {
		if slices.Contains(pids, unwanted) {
			t.Errorf("did not expect non-shim PID %s in %v", unwanted, pids)
		}
	}
}

func TestParseProcessListAndShimChildren_DetachedSnapMount(t *testing.T) {
	g := NewWithT(t)

	raw := `    1     0 /sbin/init
  500     1 /snap/k8s/5832/bin/containerd-shim-runc-v2 -namespace k8s.io -id c1
  501   500 /pause
  502   501 /usr/bin/app
  600     1 /snap/k8s/5832/bin/containerd-shim-runc-v2 -namespace k8s.io -id c2
  601   600 /pause
  700     1 /usr/bin/containerd-shim-runc-v2 -namespace k8s.io
  701   700 /pause
`

	snapFile, err := os.CreateTemp(t.TempDir(), "snap-shim-*")
	g.Expect(err).NotTo(HaveOccurred())
	_ = snapFile.Close()
	snapInfo, err := os.Stat(snapFile.Name())
	g.Expect(err).NotTo(HaveOccurred())

	origStat := osStat
	origStatfsType := statfsType
	t.Cleanup(func() {
		osStat = origStat
		statfsType = origStatfsType
	})
	osStat = func(path string) (os.FileInfo, error) {
		if path == "/proc/500/exe" || path == "/proc/600/exe" {
			return snapInfo, nil
		}
		return nil, os.ErrNotExist
	}
	statfsType = func(path string) (int64, error) {
		switch path {
		case "/proc/500/exe":
			return unix.SQUASHFS_MAGIC, nil
		case "/proc/600/exe":
			return unix.FUSE_SUPER_MAGIC, nil
		default:
			return 0, os.ErrNotExist
		}
	}

	procs := parseProcessList(raw)
	readlink := func(path string) (string, error) {
		switch path {
		case "/proc/500/exe":
			return "/bin/containerd-shim-runc-v2", nil
		case "/proc/600/exe":
			return "/bin/containerd-shim-runc-v2 (deleted)", nil
		case "/proc/700/exe":
			return "/usr/bin/containerd-shim-runc-v2", nil
		default:
			return "", os.ErrNotExist
		}
	}

	shimPIDs := findShimPIDsWithReadlink(procs, readlink)
	g.Expect(shimPIDs).To(Equal([]string{"500", "600"}))

	allPIDs := findAllChildren(shimPIDs, makeShallowChildPIDs(procs))
	// post-order: descendants before parent shim
	g.Expect(allPIDs).To(Equal([]string{"502", "501", "500", "601", "600"}))
}

func TestRunningContainerdShimPIDsDoesNotInvokeBash(t *testing.T) {
	psPath, err := exec.LookPath("ps")
	if err != nil {
		t.Skip("ps binary not available")
	}

	dir := t.TempDir()
	if err := os.Symlink(psPath, filepath.Join(dir, "ps")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bash"), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	if _, err := RunningContainerdShimPIDs(context.Background()); err != nil {
		t.Fatalf("expected direct ps execution to succeed without bash, got: %v", err)
	}
}

func TestReBinaryName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid k8s shim path",
			input: "/snap/k8s/123/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "valid k8s shim path with snapd prefix",
			input: "/var/lib/snapd/snap/k8s/current/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "valid k8s shim command with arguments",
			input: "/snap/k8s/123/bin/containerd-shim-runc-v2 -namespace k8s.io -id foo",
			want:  true,
		},
		{
			name:  "valid parallel instance k8s_inst shim path",
			input: "/snap/k8s_inst/456/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "valid parallel instance with hyphens in key",
			input: "/snap/k8s_worker-1/456/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "valid parallel instance with snapd prefix and args",
			input: "/var/lib/snapd/snap/k8s_2/current/bin/containerd-shim-runc-v2 -id bar",
			want:  true,
		},
		{
			name:  "unrelated pause process",
			input: "/pause",
			want:  false,
		},
		{
			name:  "unrelated microk8s snap shim",
			input: "/snap/microk8s/123/bin/containerd-shim-runc-v2",
			want:  false,
		},
		{
			name:  "unrelated docker snap shim",
			input: "/snap/docker/123/bin/containerd-shim-runc-v2",
			want:  false,
		},
		{
			name:  "unrelated hyphenated snap name",
			input: "/snap/k8s-custom/123/bin/containerd-shim-runc-v2",
			want:  false,
		},
		{
			name:  "binary name with invalid suffix",
			input: "/snap/k8s/123/bin/containerd-shim-runc-v2-fake",
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(reBinaryName.MatchString(tc.input)).To(Equal(tc.want))
		})
	}
}

func TestReBinaryExe(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "exact k8s shim path",
			input: "/snap/k8s/123/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "exact k8s shim path with snapd prefix",
			input: "/var/lib/snapd/snap/k8s/current/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "exact parallel instance with hyphenated key",
			input: "/snap/k8s_worker-1/456/bin/containerd-shim-runc-v2",
			want:  true,
		},
		{
			name:  "rejects trailing arguments or spaces",
			input: "/snap/k8s/123/bin/containerd-shim-runc-v2 -namespace k8s.io",
			want:  false,
		},
		{
			name:  "rejects trailing non-exact path components",
			input: "/snap/k8s/123/bin/containerd-shim-runc-v2 something",
			want:  false,
		},
		{
			name:  "rejects unrelated snap",
			input: "/snap/microk8s/123/bin/containerd-shim-runc-v2",
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(reBinaryExe.MatchString(tc.input)).To(Equal(tc.want))
		})
	}
}

func TestFindShimPIDsWithReadlink(t *testing.T) {
	snapFile, err := os.CreateTemp(t.TempDir(), "snap-shim-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = snapFile.Close()
	snapInfo, err := os.Stat(snapFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	hostFile, err := os.CreateTemp(t.TempDir(), "host-shim-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = hostFile.Close()
	hostInfo, err := os.Stat(hostFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		procs      map[string]processInfo
		readlinks  map[string]string
		readErrors map[string]error
		stats      map[string]os.FileInfo
		fsTypes    map[string]int64
		wantPIDs   []string
	}{
		{
			name: "matches valid k8s and instance shims including hyphenated keys",
			procs: map[string]processInfo{
				"101": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -namespace k8s.io -id c1",
					parentPID: "1",
				},
				"102": {
					command:   "/var/lib/snapd/snap/k8s_worker-1/current/bin/containerd-shim-runc-v2 -id c2",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/101/exe": "/snap/k8s/123/bin/containerd-shim-runc-v2",
				"/proc/102/exe": "/var/lib/snapd/snap/k8s_worker-1/current/bin/containerd-shim-runc-v2",
			},
			wantPIDs: []string{"101", "102"},
		},
		{
			name: "matches valid shim with deleted suffix",
			procs: map[string]processInfo{
				"103": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -id c3",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/103/exe": "/snap/k8s/123/bin/containerd-shim-runc-v2 (deleted)",
			},
			wantPIDs: []string{"103"},
		},
		{
			name: "matches k8s and instance shims on lazy-unmounted snap revision",
			procs: map[string]processInfo{
				"104": {
					command:   "/snap/k8s/5834/bin/containerd-shim-runc-v2 -namespace k8s.io -id c5 -address /run/containerd/containerd.sock",
					parentPID: "1",
				},
				"105": {
					command:   "/var/lib/snapd/snap/k8s_worker-1/5822/bin/containerd-shim-runc-v2 -id c6",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/104/exe": "/bin/containerd-shim-runc-v2",
				"/proc/105/exe": "/bin/containerd-shim-runc-v2 (deleted)",
			},
			stats: map[string]os.FileInfo{
				"/proc/104/exe": snapInfo,
				"/proc/105/exe": snapInfo,
			},
			fsTypes: map[string]int64{
				"/proc/104/exe": unix.SQUASHFS_MAGIC,
				"/proc/105/exe": unix.FUSE_SUPER_MAGIC,
			},
			wantPIDs: []string{"104", "105"},
		},
		{
			name: "does not match non-k8s shim with detached /bin/containerd-shim-runc-v2 exe",
			procs: map[string]processInfo{
				"203": {
					command:   "/snap/microk8s/5834/bin/containerd-shim-runc-v2 -namespace k8s.io -id c7",
					parentPID: "1",
				},
				"204": {
					command:   "/bin/containerd-shim-runc-v2 -namespace default -id c8",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/203/exe": "/bin/containerd-shim-runc-v2",
				"/proc/204/exe": "/bin/containerd-shim-runc-v2",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match host /bin/containerd-shim-runc-v2 spoofing snap argv0",
			procs: map[string]processInfo{
				"205": {
					command:   "/snap/k8s/5834/bin/containerd-shim-runc-v2 -namespace k8s.io -id spoof-ext4",
					parentPID: "1",
				},
				"206": {
					command:   "/snap/k8s/5834/bin/containerd-shim-runc-v2 -namespace k8s.io -id spoof-samefile",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/205/exe": "/bin/containerd-shim-runc-v2",
				"/proc/206/exe": "/bin/containerd-shim-runc-v2",
			},
			stats: map[string]os.FileInfo{
				"/proc/205/exe":                snapInfo,
				"/proc/206/exe":                hostInfo,
				"/bin/containerd-shim-runc-v2": hostInfo,
			},
			fsTypes: map[string]int64{
				"/proc/205/exe": unix.EXT4_SUPER_MAGIC,
				"/proc/206/exe": unix.SQUASHFS_MAGIC,
			},
			wantPIDs: nil,
		},
		{
			name: "does not match unrelated pause process",
			procs: map[string]processInfo{
				"201": {
					command:   "/pause",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/201/exe": "/pause",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match unrelated microk8s snap shim",
			procs: map[string]processInfo{
				"202": {
					command:   "/snap/microk8s/123/bin/containerd-shim-runc-v2 -id c4",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/202/exe": "/snap/microk8s/123/bin/containerd-shim-runc-v2",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match spoofed argv0 where proc exe does not match",
			procs: map[string]processInfo{
				"301": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -id spoofed",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/301/exe": "/usr/bin/python3",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match host containerd on /usr/bin/containerd-shim-runc-v2 with host command line",
			procs: map[string]processInfo{
				"503": {
					command:   "/bin/containerd-shim-runc-v2 -namespace k8s.io -id c3",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/503/exe": "/usr/bin/containerd-shim-runc-v2",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match host containerd on /usr/bin/containerd-shim-runc-v2 even with spoofed snap command line",
			procs: map[string]processInfo{
				"504": {
					command:   "/snap/k8s/5832/bin/containerd-shim-runc-v2 -namespace k8s.io -id c4",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/504/exe": "/usr/bin/containerd-shim-runc-v2",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match plain containerd-shim-runc-v2 command line with exe /bin/containerd-shim-runc-v2",
			procs: map[string]processInfo{
				"505": {
					command:   "containerd-shim-runc-v2 -namespace k8s.io -id c5",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/505/exe": "/bin/containerd-shim-runc-v2",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match non-exact proc exe with trailing whitespace or args",
			procs: map[string]processInfo{
				"302": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2",
					parentPID: "1",
				},
			},
			readlinks: map[string]string{
				"/proc/302/exe": "/snap/k8s/123/bin/containerd-shim-runc-v2 something",
			},
			wantPIDs: nil,
		},
		{
			name: "does not match when process exited (os.ErrNotExist)",
			procs: map[string]processInfo{
				"401": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -id exited",
					parentPID: "1",
				},
			},
			readErrors: map[string]error{
				"/proc/401/exe": os.ErrNotExist,
			},
			wantPIDs: nil,
		},
		{
			name: "does not match when readlink permission denied (os.ErrPermission)",
			procs: map[string]processInfo{
				"402": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -id forbidden",
					parentPID: "1",
				},
			},
			readErrors: map[string]error{
				"/proc/402/exe": os.ErrPermission,
			},
			wantPIDs: nil,
		},
		{
			name: "does not match when readlink fails with generic error",
			procs: map[string]processInfo{
				"403": {
					command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -id generic-err",
					parentPID: "1",
				},
			},
			readErrors: map[string]error{
				"/proc/403/exe": errors.New("i/o error"),
			},
			wantPIDs: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			origStat := osStat
			origStatfsType := statfsType
			t.Cleanup(func() {
				osStat = origStat
				statfsType = origStatfsType
			})

			osStat = func(path string) (os.FileInfo, error) {
				if info, ok := tc.stats[path]; ok {
					return info, nil
				}
				return nil, os.ErrNotExist
			}
			statfsType = func(path string) (int64, error) {
				if fst, ok := tc.fsTypes[path]; ok {
					return fst, nil
				}
				return 0, os.ErrNotExist
			}

			mockReadlink := func(path string) (string, error) {
				if rlErr, ok := tc.readErrors[path]; ok {
					return "", rlErr
				}
				if target, ok := tc.readlinks[path]; ok {
					return target, nil
				}
				return "", os.ErrNotExist
			}

			pids := findShimPIDsWithReadlink(tc.procs, mockReadlink)
			g.Expect(pids).To(Equal(tc.wantPIDs))
		})
	}
}

func TestFindAllChildren_PostOrder(t *testing.T) {
	g := NewWithT(t)

	// Process tree:
	// Shim 10 has container process 20 and container process 21.
	// Container process 20 has inner workload process 30.
	// Container process 21 has inner workload process 31.
	//
	// Post-order requirement:
	// Descendants (30, 31, 20, 21) MUST appear before parent shim (10).
	// Child 30 MUST appear before its parent 20.
	// Child 31 MUST appear before its parent 21.
	shallow := map[string][]string{
		"10": {"20", "21"},
		"20": {"30"},
		"21": {"31"},
	}

	result := findAllChildren([]string{"10"}, shallow)
	g.Expect(result).To(Equal([]string{"30", "20", "31", "21", "10"}))
}

func TestFindAllChildren_CycleAndDuplicates(t *testing.T) {
	g := NewWithT(t)

	t.Run("handles cycle without infinite loop or duplicates", func(t *testing.T) {
		shallow := map[string][]string{
			"10": {"20"},
			"20": {"30"},
			"30": {"20"}, // cycle back to 20
		}

		result := findAllChildren([]string{"10"}, shallow)
		g.Expect(result).To(Equal([]string{"30", "20", "10"}))
	})

	t.Run("handles self loop", func(t *testing.T) {
		shallow := map[string][]string{
			"10": {"10"},
		}

		result := findAllChildren([]string{"10"}, shallow)
		g.Expect(result).To(Equal([]string{"10"}))
	})

	t.Run("deduplicates start PIDs and shared descendants", func(t *testing.T) {
		shallow := map[string][]string{
			"10": {"20"},
			"11": {"20"},
			"20": {"30"},
		}

		result := findAllChildren([]string{"10", "11", "10"}, shallow)
		g.Expect(result).To(Equal([]string{"30", "20", "10", "11"}))
	})

	t.Run("empty start PIDs returns nil", func(t *testing.T) {
		result := findAllChildren(nil, map[string][]string{})
		g.Expect(result).To(BeNil())
	})
}

func TestMakeShallowChildPIDs(t *testing.T) {
	g := NewWithT(t)

	procs := map[string]processInfo{
		"10": {parentPID: "1", command: "parent"},
		"20": {parentPID: "10", command: "child1"},
		"21": {parentPID: "10", command: "child2"},
		"30": {parentPID: "20", command: "grandchild"},
	}

	shallow := makeShallowChildPIDs(procs)
	g.Expect(shallow["10"]).To(Equal([]string{"20", "21"}))
	g.Expect(shallow["20"]).To(Equal([]string{"30"}))
	g.Expect(shallow["1"]).To(Equal([]string{"10"}))
	g.Expect(shallow["30"]).To(BeNil())
}

func TestRunningContainerdShimPIDs_LiveHost(t *testing.T) {
	g := NewWithT(t)

	// Verify RunningContainerdShimPIDs executes successfully against host processes without error.
	_, err := RunningContainerdShimPIDs(context.Background())
	g.Expect(err).NotTo(HaveOccurred())
}

func TestRunningContainerdShimPIDs_Controlled(t *testing.T) {
	g := NewWithT(t)

	origListProcesses := listProcesses
	origReadlink := osReadlink
	t.Cleanup(func() {
		listProcesses = origListProcesses
		osReadlink = origReadlink
	})

	listProcesses = func(ctx context.Context) (map[string]processInfo, error) {
		return map[string]processInfo{
			"10": {
				command:   "/snap/k8s/123/bin/containerd-shim-runc-v2 -namespace k8s.io -id c1",
				parentPID: "1",
			},
			"20": {
				command:   "/pause",
				parentPID: "10",
			},
			"30": {
				command:   "/bin/my-app",
				parentPID: "20",
			},
			"99": {
				command:   "/snap/microk8s/123/bin/containerd-shim-runc-v2 -id c2",
				parentPID: "1",
			},
		}, nil
	}

	osReadlink = func(path string) (string, error) {
		switch path {
		case "/proc/10/exe":
			return "/snap/k8s/123/bin/containerd-shim-runc-v2", nil
		case "/proc/99/exe":
			return "/snap/microk8s/123/bin/containerd-shim-runc-v2", nil
		default:
			return "", os.ErrNotExist
		}
	}

	pids, err := RunningContainerdShimPIDs(context.Background())
	g.Expect(err).NotTo(HaveOccurred())
	// Post-order: descendant 30, child 20, parent shim 10. Unrelated microk8s 99 is excluded.
	g.Expect(pids).To(Equal([]string{"30", "20", "10"}))
}

func TestRunningContainerdShimPIDs_Error(t *testing.T) {
	g := NewWithT(t)

	origListProcesses := listProcesses
	t.Cleanup(func() {
		listProcesses = origListProcesses
	})

	listProcesses = func(ctx context.Context) (map[string]processInfo, error) {
		return nil, errors.New("simulated ps failure")
	}

	_, err := RunningContainerdShimPIDs(context.Background())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to list running host processes"))
}

func TestRunningContainerdShimPIDs_DetachedSnapMount(t *testing.T) {
	if _, err := os.Stat("/snap"); err != nil {
		t.Skipf("/snap directory not present: %v", err)
	}
	unsharePath, err := exec.LookPath("unshare")
	if err != nil {
		t.Skipf("unshare binary not available: %v", err)
	}
	mksquashfsPath, err := exec.LookPath("mksquashfs")
	if err != nil {
		t.Skipf("mksquashfs binary not available: %v", err)
	}
	snapfusePath, err := exec.LookPath("snapfuse")
	if err != nil {
		t.Skipf("snapfuse binary not available: %v", err)
	}
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep binary not available: %v", err)
	}

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "snapsrc")
	if err := os.MkdirAll(filepath.Join(srcDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	sleepBytes, err := os.ReadFile(sleepPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "bin", "containerd-shim-runc-v2"), sleepBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	snapImg := filepath.Join(tmpDir, "k8s.snap")
	if out, mkErr := exec.CommandContext(t.Context(), mksquashfsPath, srcDir, snapImg, "-noappend", "-quiet").CombinedOutput(); mkErr != nil {
		t.Fatalf("mksquashfs failed: %v (%s)", mkErr, string(out))
	}

	script := fmt.Sprintf(`set -eu
mount -t tmpfs tmpfs /snap
mkdir -p /snap/k8s/5834 /snap/k8s/9999
%q %q /snap/k8s/5834
/snap/k8s/5834/bin/containerd-shim-runc-v2 60 &
snap_pid=$!
umount -l /snap/k8s/5834

mount --bind %q /snap/k8s/9999
/snap/k8s/9999/bin/containerd-shim-runc-v2 60 &
spoof_pid=$!
umount -l /snap/k8s/9999

echo "PIDS=$snap_pid,$spoof_pid"
wait "$snap_pid" "$spoof_pid" || true
`, snapfusePath, snapImg, srcDir)

	cmd := exec.CommandContext(t.Context(), unsharePath, "-Umr", "sh", "-c", script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Skipf("failed to start unshare: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		_ = cmd.Wait()
		t.Skipf("unprivileged user/mount/fuse unavailable: %s", stderr.String())
	}
	line := scanner.Text()
	rawPIDs, ok := strings.CutPrefix(line, "PIDS=")
	parts := strings.Split(rawPIDs, ",")
	if !ok || len(parts) != 2 {
		t.Fatalf("unexpected helper output %q (stderr=%s)", line, stderr.String())
	}
	snapPID, spoofPID := parts[0], parts[1]
	t.Cleanup(func() {
		for _, pid := range []string{snapPID, spoofPID} {
			if p, findErr := os.FindProcess(mustAtoi(t, pid)); findErr == nil {
				_ = p.Kill()
			}
		}
	})

	g := NewWithT(t)
	for _, pid := range []string{snapPID, spoofPID} {
		exeTarget, readErr := os.Readlink(fmt.Sprintf("/proc/%s/exe", pid))
		g.Expect(readErr).NotTo(HaveOccurred())
		g.Expect(exeTarget).To(Equal("/bin/containerd-shim-runc-v2"))
	}

	pids, err := RunningContainerdShimPIDs(t.Context())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(pids).To(ContainElement(snapPID))
	g.Expect(pids).NotTo(ContainElement(spoofPID))
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("invalid pid %q: %v", s, err)
	}
	return n
}
