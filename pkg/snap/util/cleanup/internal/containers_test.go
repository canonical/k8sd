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
)

func TestKillProcessesWaitsForExit(t *testing.T) {
	g := NewWithT(t)

	cmd := exec.Command("sleep", "60")
	g.Expect(cmd.Start()).To(Succeed())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	killProcesses(context.Background(), []int{cmd.Process.Pid}, 5*time.Second)

	// The process has already exited when killProcesses returns. It stays a zombie until
	// this test, its parent, reaps it.
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cmd.Process.Pid))
	g.Expect(err).ToNot(HaveOccurred())
	state := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))[0]
	g.Expect(state).To(Equal("Z"))
}
