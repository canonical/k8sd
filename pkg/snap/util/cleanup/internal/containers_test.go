package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func testSignaler(kill func(int, unix.Signal) error) func(int, string, func(int) (string, error)) error {
	return func(pid int, startTime string, identity func(int) (string, error)) error {
		start, err := identity(pid)
		if err != nil {
			return err
		}
		if start != startTime {
			return fmt.Errorf("container PID %d replaced before signal", pid)
		}
		return kill(pid, unix.SIGKILL)
	}
}

func TestContainerProcessIdentityAndCapture(t *testing.T) {
	pid := os.Getpid()
	start, err := containerProcessIdentity(pid)
	if err != nil || start == "" {
		t.Fatalf("own process identity: %q err=%v", start, err)
	}
	got, err := captureContainerProcesses([]string{strconv.Itoa(pid), "bad", "0", "1", "2147483647"})
	if err == nil {
		t.Fatal("invalid PIDs were not reported")
	}
	if !reflect.DeepEqual(got, []containerProcess{{pid, start}}) {
		t.Fatalf("captured processes: %+v", got)
	}
}

func TestRemoveContainersWithNoOwnedProcesses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	owned, err := RemoveContainers(context.Background())
	if err != nil || len(owned) != 0 {
		t.Fatalf("empty cleanup: %v err=%v", owned, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\nexit 18\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveContainers(context.Background()); err == nil {
		t.Fatal("ignored process enumeration failure")
	}
}

func TestKillContainerProcessesReportsIdentityErrors(t *testing.T) {
	want := errors.New("cannot inspect process")
	signaler := testSignaler(func(int, unix.Signal) error { t.Fatal("signaled unverified process"); return nil })
	_, err := killContainerProcesses(context.Background(), []containerProcess{{42, "1"}}, signaler, func(int) (string, error) { return "", want })
	if !errors.Is(err, want) {
		t.Fatalf("identity error lost: %v", err)
	}
	err = waitContainerExit(context.Background(), containerProcess{42, "1"}, func(int) (string, error) { return "", want })
	if !errors.Is(err, want) {
		t.Fatalf("wait identity error lost: %v", err)
	}
}

func TestContainerNetworkNamespacesRejectsUnreadableDirectories(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := containerNetworkNamespaces([]string{"42"}, file, file); err == nil {
		t.Fatal("invalid namespace roots were ignored")
	}
	owned, err := containerNetworkNamespaces(nil, t.TempDir(), file+"-absent")
	if err != nil || len(owned) != 0 {
		t.Fatalf("absent namespace directory: %v err=%v", owned, err)
	}
}

func TestContainerNetworkNamespacesPreservesOtherRuntimes(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	netns := filepath.Join(root, "netns")
	if err := os.MkdirAll(filepath.Join(proc, "42", "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "43", "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "43", "ns", "net"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(netns, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cni-owned", "cni-other"} {
		if err := os.WriteFile(filepath.Join(netns, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(netns, "cni-owned"), filepath.Join(proc, "42", "ns", "net")); err != nil {
		t.Fatal(err)
	}
	got, err := containerNetworkNamespaces([]string{"42", "43"}, proc, netns)
	if err != nil || !reflect.DeepEqual(got, map[string]bool{"cni-owned": true}) {
		t.Fatalf("owned namespaces = %v, err=%v", got, err)
	}
}

func TestKillContainerProcessesUsesKillAndContinuesAfterFailure(t *testing.T) {
	var called []int
	processes := []containerProcess{{-1, "1"}, {0, "1"}, {1, "1"}, {22, "1"}, {23, "1"}, {24, "1"}}
	signaler := testSignaler(func(pid int, signal unix.Signal) error {
		if signal != unix.SIGKILL {
			t.Fatalf("unexpected signal %v", signal)
		}
		called = append(called, pid)
		if pid == 22 {
			return unix.EPERM
		}
		if pid == 23 {
			return unix.ESRCH
		}
		return nil
	})
	_, err := killContainerProcesses(context.Background(), processes, signaler, func(pid int) (string, error) {
		for _, calledPID := range called {
			if calledPID == pid {
				return "", nil
			}
		}
		return "1", nil
	})
	if !errors.Is(err, unix.EPERM) {
		t.Fatalf("want signal failure, got %v", err)
	}
	if !reflect.DeepEqual(called, []int{22, 23, 24}) {
		t.Fatalf("unsafe/incomplete PIDs %v", called)
	}
}

func TestKillContainerProcessesWaitsForExit(t *testing.T) {
	checks := 0
	signaler := testSignaler(func(int, unix.Signal) error { return nil })
	_, err := killContainerProcesses(context.Background(), []containerProcess{{42, "1"}}, signaler, func(int) (string, error) {
		checks++
		if checks < 3 {
			return "1", nil
		}
		return "", nil
	})
	if err != nil || checks != 3 {
		t.Fatalf("cleanup returned before process exit: checks=%d err=%v", checks, err)
	}
}

func TestKillContainerProcessesHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	signaler := testSignaler(func(int, unix.Signal) error { return nil })
	_, err := killContainerProcesses(ctx, []containerProcess{{42, "1"}}, signaler, func(int) (string, error) { return "1", nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
}

func TestKillContainerProcessesDoesNotSignalReusedPID(t *testing.T) {
	signaler := testSignaler(func(int, unix.Signal) error { t.Fatal("signaled reused PID"); return nil })
	_, err := killContainerProcesses(context.Background(), []containerProcess{{42, "1"}}, signaler, func(int) (string, error) { return "2", nil })
	if err == nil || !strings.Contains(err.Error(), "replaced before signal") {
		t.Fatalf("want error for reused PID, got: %v", err)
	}
}

func TestKillContainerProcessesDoesNotWaitForReusedPID(t *testing.T) {
	calls := 0
	signaler := testSignaler(func(int, unix.Signal) error { return nil })
	_, err := killContainerProcesses(context.Background(), []containerProcess{{42, "1"}}, signaler, func(int) (string, error) {
		calls++
		if calls == 1 {
			return "1", nil
		}
		return "2", nil
	})
	if err == nil || !strings.Contains(err.Error(), "replaced by another process") {
		t.Fatalf("expected error for PID replaced while waiting, got: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}
