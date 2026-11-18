package internal

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestRemoveContainers_MultiPassConvergence(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch calls {
		case 1:
			return []string{"100", "101"}, nil
		case 2:
			return []string{"102"}, nil
		default:
			return nil, nil
		}
	}

	mockKill := func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		return nil
	}

	// Once killed, process is no longer alive
	mockIsAlive := func(pid int) bool {
		mu.Lock()
		defer mu.Unlock()
		return !slices.Contains(killed, pid)
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(3))
	g.Expect(killed).To(Equal([]int{100, 101, 102}))
}

func TestRemoveContainers_ReparentedChildProcessKeptUntilExit(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int
	childAlive := true

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			// Pass 1: returns shim 500 and child 501
			return []string{"500", "501"}, nil
		}
		// Pass 2 and beyond: shim 500 has exited so RunningContainerdShimPIDs returns 0 PIDs,
		// but child 501 is reparented to init and still running!
		return nil, nil
	}

	mockKill := func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		// On second kill, child finally terminates
		if pid == 501 && calls >= 2 {
			childAlive = false
		}
		return nil
	}

	mockIsAlive := func(pid int) bool {
		mu.Lock()
		defer mu.Unlock()
		if pid == 500 {
			// Shim exited after pass 1
			return false
		}
		if pid == 501 {
			return childAlive
		}
		return false
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	// Must have completed 3 passes:
	// Pass 1: saw 500, 501. Killed both.
	// Pass 2: getPIDs returned [], but 501 still alive in trackedPIDs. Killed 501 again -> dies.
	// Pass 3: getPIDs returned [], trackedPIDs empty -> terminates.
	g.Expect(calls).To(Equal(3))
	g.Expect(killed).To(Equal([]int{500, 501, 501}))
	g.Expect(childAlive).To(BeFalse())
}

func TestRemoveContainers_ErrProcessDoneAndESRCHIgnored(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []string{"200", "201", "202"}, nil
		}
		return nil, nil
	}

	mockKill := func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		switch pid {
		case 200:
			return os.ErrProcessDone
		case 201:
			return syscall.ESRCH
		case 202:
			return nil
		default:
			return errors.New("unexpected pid")
		}
	}

	mockIsAlive := func(pid int) bool {
		return false
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(2))
	g.Expect(killed).To(Equal([]int{200, 201, 202}))
}

func TestRemoveContainers_BoundedByContextDeadline(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return []string{"300"}, nil
	}

	mockKill := func(pid int) error {
		return nil
	}

	mockIsAlive := func(pid int) bool {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, 20*time.Millisecond, 5*time.Second)
	elapsed := time.Since(start)

	g.Expect(elapsed).To(BeNumerically("<", 500*time.Millisecond))
	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(BeNumerically(">=", 1))
}

func TestRemoveContainers_BoundedByMaxWaitTimeout(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return []string{"400"}, nil
	}

	mockKill := func(pid int) error {
		return nil
	}

	mockIsAlive := func(pid int) bool {
		return true
	}

	ctx := context.Background()
	start := time.Now()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, 20*time.Millisecond, 60*time.Millisecond)
	elapsed := time.Since(start)

	g.Expect(elapsed).To(BeNumerically("<", 500*time.Millisecond))
	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(BeNumerically(">=", 1))
}

func TestRemoveContainers_PreCancelledContext(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return []string{"500"}, nil
	}
	mockKill := func(pid int) error {
		return nil
	}
	mockIsAlive := func(pid int) bool {
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, 10*time.Millisecond, 2*time.Second)
	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(0))
}

func TestRemoveContainers_ExitImmediatelyWhenNoPIDs(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil, nil
	}
	mockKill := func(pid int) error {
		t.Fatalf("kill should not be called")
		return nil
	}
	mockIsAlive := func(pid int) bool {
		return false
	}

	start := time.Now()
	removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, 100*time.Millisecond, 2*time.Second)
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(1))
	g.Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond))
}

func TestRemoveContainers_GetPIDsError(t *testing.T) {
	g := NewWithT(t)

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		return nil, errors.New("ps failed")
	}
	mockKill := func(pid int) error {
		t.Fatalf("kill should not be called")
		return nil
	}
	mockIsAlive := func(pid int) bool {
		return false
	}

	removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, 10*time.Millisecond, 2*time.Second)
	g.Expect(true).To(BeTrue())
}

func TestRemoveContainers_NonIntegerPID(t *testing.T) {
	g := NewWithT(t)

	var calls int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		calls++
		if calls == 1 {
			return []string{"not-a-number", "600"}, nil
		}
		return nil, nil
	}
	var killed []int
	mockKill := func(pid int) error {
		killed = append(killed, pid)
		return nil
	}
	mockIsAlive := func(pid int) bool {
		return false
	}

	removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, 10*time.Millisecond, 2*time.Second)
	g.Expect(killed).To(Equal([]int{600}))
}

func TestRemoveContainers_PublicEntryPoint(t *testing.T) {
	g := NewWithT(t)

	origGetPIDs := runningContainerdShimPIDs
	origKill := killProcessPID
	origIsAlive := isProcessAlive
	origInterval := containersPollInterval
	origTimeout := containersMaxWaitTimeout

	t.Cleanup(func() {
		runningContainerdShimPIDs = origGetPIDs
		killProcessPID = origKill
		isProcessAlive = origIsAlive
		containersPollInterval = origInterval
		containersMaxWaitTimeout = origTimeout
	})

	var mu sync.Mutex
	var calls int
	var killed []int

	runningContainerdShimPIDs = func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []string{"700"}, nil
		}
		return nil, nil
	}

	killProcessPID = func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		return nil
	}

	isProcessAlive = func(pid int) bool {
		return false
	}

	containersPollInterval = 10 * time.Millisecond
	containersMaxWaitTimeout = 1 * time.Second

	RemoveContainers(context.Background())

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(2))
	g.Expect(killed).To(Equal([]int{700}))
}

func TestDefaultIsProcessAlive(t *testing.T) {
	g := NewWithT(t)

	// Current process is alive
	g.Expect(defaultIsProcessAlive(os.Getpid())).To(BeTrue())

	// Non-existent PID should be dead (ESRCH)
	// PID 4194304 is typically greater than pid_max on Linux
	g.Expect(defaultIsProcessAlive(4194300)).To(BeFalse())
}
