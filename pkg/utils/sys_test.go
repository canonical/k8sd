package utils

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCleanHostEnviron(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name: "snap environment with empty REAL_LD_LIBRARY_PATH strips LD_LIBRARY_PATH and restores REAL_PATH",
			input: []string{
				"SNAP=/snap/k8s/current",
				"REAL_LD_LIBRARY_PATH=",
				"LD_LIBRARY_PATH=/snap/k8s/current/lib:/snap/k8s/current/lib/x86_64-linux-gnu",
				"REAL_PATH=/usr/bin:/bin",
				"PATH=/snap/k8s/current/bin:/usr/bin:/bin",
				"HOME=/root",
			},
			expected: []string{
				"SNAP=/snap/k8s/current",
				"REAL_LD_LIBRARY_PATH=",
				"REAL_PATH=/usr/bin:/bin",
				"PATH=/usr/bin:/bin",
				"HOME=/root",
			},
		},
		{
			name: "snap environment with non-empty REAL_LD_LIBRARY_PATH restores LD_LIBRARY_PATH",
			input: []string{
				"SNAP=/snap/k8s/current",
				"REAL_LD_LIBRARY_PATH=/usr/local/lib",
				"LD_LIBRARY_PATH=/snap/k8s/current/lib:/snap/k8s/current/lib/x86_64-linux-gnu",
				"REAL_PATH=/usr/bin:/bin",
				"PATH=/snap/k8s/current/bin:/usr/bin:/bin",
			},
			expected: []string{
				"SNAP=/snap/k8s/current",
				"REAL_LD_LIBRARY_PATH=/usr/local/lib",
				"LD_LIBRARY_PATH=/usr/local/lib",
				"REAL_PATH=/usr/bin:/bin",
				"PATH=/usr/bin:/bin",
			},
		},
		{
			name: "snap environment without REAL_LD_LIBRARY_PATH or REAL_PATH strips snap dirs",
			input: []string{
				"SNAP=/snap/k8s/123",
				"LD_LIBRARY_PATH=/snap/k8s/current/lib",
				"PATH=/snap/k8s/current/bin:/snap/k8s/123/sbin:/usr/bin:/snap/bin",
			},
			expected: []string{
				"SNAP=/snap/k8s/123",
				"PATH=/usr/bin:/snap/bin",
			},
		},
		{
			name: "nested snap run strips snap entries from polluted REAL_PATH and REAL_LD_LIBRARY_PATH",
			input: []string{
				"SNAP=/snap/k8s/123",
				"SNAP_LIBRARY_PATH=/var/lib/snapd/lib/gl",
				"REAL_LD_LIBRARY_PATH=/var/lib/snapd/lib/gl:/snap/k8s/current/lib/aarch64-linux-gnu:/opt/host/lib",
				"LD_LIBRARY_PATH=/var/lib/snapd/lib/gl:/snap/k8s/current/lib/aarch64-linux-gnu:/opt/host/lib",
				"REAL_PATH=/snap/k8s/current/usr/bin:/snap/k8s/current/bin:/usr/bin:/bin",
				"PATH=/snap/k8s/current/usr/bin:/snap/k8s/current/bin:/usr/bin:/bin",
			},
			expected: []string{
				"SNAP=/snap/k8s/123",
				"SNAP_LIBRARY_PATH=/var/lib/snapd/lib/gl",
				"REAL_LD_LIBRARY_PATH=/var/lib/snapd/lib/gl:/snap/k8s/current/lib/aarch64-linux-gnu:/opt/host/lib",
				"LD_LIBRARY_PATH=/opt/host/lib",
				"REAL_PATH=/snap/k8s/current/usr/bin:/snap/k8s/current/bin:/usr/bin:/bin",
				"PATH=/usr/bin:/bin",
			},
		},
		{
			name: "deduplicates repeated LD_LIBRARY_PATH and PATH entries",
			input: []string{
				"SNAP=/snap/k8s/current",
				"REAL_LD_LIBRARY_PATH=/usr/local/lib",
				"LD_LIBRARY_PATH=/snap/k8s/current/lib",
				"LD_LIBRARY_PATH=/another/snap/lib",
				"REAL_PATH=/usr/bin",
				"PATH=/snap/k8s/current/bin",
				"PATH=/usr/bin",
			},
			expected: []string{
				"SNAP=/snap/k8s/current",
				"REAL_LD_LIBRARY_PATH=/usr/local/lib",
				"LD_LIBRARY_PATH=/usr/local/lib",
				"REAL_PATH=/usr/bin",
				"PATH=/usr/bin",
			},
		},
		{
			name: "snap environment with snap-only PATH and no REAL_PATH omits PATH rather than restoring snap PATH",
			input: []string{
				"SNAP=/snap/k8s/123",
				"PATH=/snap/k8s/current/bin:/snap/k8s/123/sbin",
				"HOME=/root",
			},
			expected: []string{
				"SNAP=/snap/k8s/123",
				"HOME=/root",
			},
		},
		{
			name: "outside snap environment preserves LD_LIBRARY_PATH and PATH",
			input: []string{
				"LD_LIBRARY_PATH=/custom/lib",
				"PATH=/usr/bin:/bin",
				"USER=ubuntu",
			},
			expected: []string{
				"LD_LIBRARY_PATH=/custom/lib",
				"PATH=/usr/bin:/bin",
				"USER=ubuntu",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := CleanHostEnviron(tt.input)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("CleanHostEnviron() mismatch\nexpected: %v\nactual:   %v", tt.expected, actual)
			}
		})
	}
}

func TestHostCommandContext_ResolvesHostBinaryOverStagedSnapBinary(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap", "k8s", "x1")
	snapBin := filepath.Join(snapRoot, "bin")
	hostBin := filepath.Join(root, "usr", "bin")
	if err := os.MkdirAll(snapBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hostBin, 0o755); err != nil {
		t.Fatal(err)
	}

	snapTool := filepath.Join(snapBin, "systemctl")
	hostTool := filepath.Join(hostBin, "systemctl")
	if err := os.WriteFile(snapTool, []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostTool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SNAP", snapRoot)
	t.Setenv("REAL_LD_LIBRARY_PATH", "")
	t.Setenv("LD_LIBRARY_PATH", filepath.Join(snapRoot, "lib"))
	t.Setenv("REAL_PATH", hostBin+":/bin")
	t.Setenv("PATH", snapBin+":"+hostBin+":/bin")

	cmd := HostCommandContext(context.Background(), "systemctl", "status")
	if cmd.Path != hostTool {
		t.Fatalf("expected cmd.Path=%q (host binary), got %q", hostTool, cmd.Path)
	}
	if len(cmd.Args) < 2 || cmd.Args[0] != "systemctl" || cmd.Args[1] != "status" {
		t.Fatalf("unexpected cmd.Args: %v", cmd.Args)
	}
	for _, kv := range cmd.Env {
		if kv == "LD_LIBRARY_PATH="+filepath.Join(snapRoot, "lib") {
			t.Fatalf("found snap LD_LIBRARY_PATH in cmd.Env: %v", cmd.Env)
		}
	}
}

func TestHostCommandContext_FallsBackToSnapBinaryWithSnapEnvWhenAbsentOnHost(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap", "k8s", "x1")
	snapSbin := filepath.Join(snapRoot, "sbin")
	emptyHostBin := filepath.Join(root, "usr", "bin")
	if err := os.MkdirAll(snapSbin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(emptyHostBin, 0o755); err != nil {
		t.Fatal(err)
	}

	snapOnlyTool := filepath.Join(snapSbin, "snap-only-tool")
	if err := os.WriteFile(snapOnlyTool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SNAP", snapRoot)
	t.Setenv("REAL_LD_LIBRARY_PATH", "")
	t.Setenv("LD_LIBRARY_PATH", filepath.Join(snapRoot, "lib"))
	t.Setenv("REAL_PATH", emptyHostBin)
	t.Setenv("PATH", snapSbin+":"+emptyHostBin)

	cmd := HostCommandContext(context.Background(), "snap-only-tool")
	if cmd.Path != snapOnlyTool {
		t.Fatalf("expected fallback cmd.Path=%q, got %q", snapOnlyTool, cmd.Path)
	}
	if cmd.Env != nil {
		t.Fatalf("expected nil cmd.Env (preserving snap LD_LIBRARY_PATH) for snap-staged fallback binary, got %v", cmd.Env)
	}
}

func TestHostCommandContext_RejectsNonSnapBinaryOutsideRealPath(t *testing.T) {
	root := t.TempDir()
	snapRoot := filepath.Join(root, "snap", "k8s", "x1")
	hostBin := filepath.Join(root, "usr", "bin")
	otherBin := filepath.Join(root, "opt", "other")
	for _, dir := range []string{snapRoot, hostBin, otherBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(otherBin, "other-tool"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SNAP", snapRoot)
	t.Setenv("REAL_LD_LIBRARY_PATH", "")
	t.Setenv("LD_LIBRARY_PATH", filepath.Join(snapRoot, "lib"))
	t.Setenv("REAL_PATH", hostBin)
	t.Setenv("PATH", otherBin+":"+hostBin)

	cmd := HostCommandContext(context.Background(), "other-tool")
	if !errors.Is(cmd.Err, exec.ErrNotFound) {
		t.Fatalf("expected exec.ErrNotFound for binary outside REAL_PATH, got cmd.Path=%q cmd.Err=%v", cmd.Path, cmd.Err)
	}
	if err := cmd.Run(); err == nil {
		t.Fatal("expected Run to fail for binary outside REAL_PATH")
	}
}

func TestRunHostCommand_MissingExecutable(t *testing.T) {
	err := RunHostCommand(context.Background(), []string{"definitely-nonexistent-host-binary-xyz"})
	if err == nil {
		t.Fatal("expected error for missing executable")
	}
}
