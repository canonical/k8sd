package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"golang.org/x/sys/unix"
)

func startSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// processState returns the state letter from /proc/<pid>/stat, e.g. "S" or "Z".
func processState(t *testing.T, pid int) string {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("failed to read stat of %d: %v", pid, err)
	}
	return strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))[0]
}

func TestKillProcesses(t *testing.T) {
	g := NewWithT(t)

	owned := startSleep(t)
	foreign := startSleep(t)
	isOwned := func(pid int) bool { return pid == owned.Process.Pid }

	netns := killProcesses(context.Background(), []int{owned.Process.Pid, foreign.Process.Pid}, isOwned, 5*time.Second)

	// The owned process has exited by the time killProcesses returns. It stays a zombie
	// until this test, its parent, reaps it.
	g.Expect(processState(t, owned.Process.Pid)).To(Equal("Z"))
	g.Expect(unix.Kill(foreign.Process.Pid, 0)).To(Succeed())
	g.Expect(processState(t, foreign.Process.Pid)).ToNot(Equal("Z"))

	// Only the owned process's network namespace is reported.
	g.Expect(netns.contains("/proc/self/ns/net")).To(BeTrue())
	g.Expect(netns).To(HaveLen(1))
}
