package shims

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
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
	pids := findAllChildren(findShimPIDs(procs), makeShallowChildPIDs(procs))

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
