package internal

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func defaultMockStat(pid int) (byte, uint64, bool) {
	return 'S', 12345, true
}

func defaultMockIsAlive(target processTarget) bool {
	return isTargetAlive(target, defaultMockStat)
}

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

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		return nil
	}

	mockIsAlive := func(target processTarget) bool {
		mu.Lock()
		defer mu.Unlock()
		return !slices.Contains(killed, target.pid)
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)

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
			// Pass 1: returns child 501 and shim 500 in post-order
			return []string{"501", "500"}, nil
		}
		// Pass 2 and beyond: shim 500 exited, but child 501 reparented and still alive
		return nil, nil
	}

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		if target.pid == 501 && calls >= 2 {
			childAlive = false
		}
		return nil
	}

	mockIsAlive := func(target processTarget) bool {
		mu.Lock()
		defer mu.Unlock()
		if target.pid == 500 {
			return false
		}
		if target.pid == 501 {
			return childAlive
		}
		return false
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(3))
	// Child 501 was killed on pass 1, then again on pass 2 until exit
	g.Expect(killed).To(Equal([]int{501, 500, 501}))
	g.Expect(childAlive).To(BeFalse())
}

func TestRemoveContainers_TransientGetPIDsErrorRetainsTrackedPIDs(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int
	p100Alive := true

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch calls {
		case 1:
			return []string{"100"}, nil
		case 2:
			// Transient error on second pass
			return nil, errors.New("transient ps error")
		default:
			return nil, nil
		}
	}

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		if target.pid == 100 && calls >= 2 {
			p100Alive = false
		}
		return nil
	}

	mockIsAlive := func(target processTarget) bool {
		mu.Lock()
		defer mu.Unlock()
		return p100Alive
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(3))
	g.Expect(killed).To(Equal([]int{100, 100}))
	g.Expect(p100Alive).To(BeFalse())
}

func TestRemoveContainers_ChildrenKilledBeforeShim(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			// RunningContainerdShimPIDs returns post-order: child2, child1, shim
			return []string{"302", "301", "300"}, nil
		}
		return nil, nil
	}

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		return nil
	}

	mockIsAlive := func(target processTarget) bool {
		return false
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	// Children must be killed BEFORE the parent shim (300)
	g.Expect(killed).To(Equal([]int{302, 301, 300}))
}

func TestRemoveContainers_ReusedPIDNotKilled(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []string{"400"}, nil
		}
		return nil, nil
	}

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		return nil
	}

	mockStat := func(pid int) (byte, uint64, bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(killed) == 0 {
			return 'S', 1000, true
		}
		// Once killed, PID 400 is recycled with a new start time
		return 'S', 9999, true
	}

	mockIsAlive := func(target processTarget) bool {
		return isTargetAlive(target, mockStat)
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, mockStat, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	// On pass 2, PID 400 has different start time so it is detected as recycled and not killed again
	g.Expect(calls).To(Equal(2))
	g.Expect(killed).To(Equal([]int{400}))
}

func TestRemoveContainers_ZombieTreatedAsDead(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed bool

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []string{"500"}, nil
		}
		return nil, nil
	}

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = true
		return nil
	}

	mockStat := func(pid int) (byte, uint64, bool) {
		mu.Lock()
		defer mu.Unlock()
		if !killed {
			return 'S', 1000, true
		}
		// After pass 1 kill, process enters Zombie ('Z') state
		return 'Z', 1000, true
	}

	mockIsAlive := func(target processTarget) bool {
		return isTargetAlive(target, mockStat)
	}

	ctx := context.Background()
	start := time.Now()
	// Should prune zombie immediately and not wait for the 5-second timeout
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, mockStat, 10*time.Millisecond, 5*time.Second)
	elapsed := time.Since(start)

	g.Expect(elapsed).To(BeNumerically("<", 500*time.Millisecond))
	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(2))
}

func TestRemoveContainers_UnverifiedStatNotTracked(t *testing.T) {
	g := NewWithT(t)

	var mu sync.Mutex
	var calls int
	var killed []int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []string{"600"}, nil
		}
		return nil, nil
	}
	mockKill := func(target processTarget) error {
		killed = append(killed, target.pid)
		return nil
	}
	// stat read fails (process disappeared between ps and stat)
	mockStat := func(pid int) (byte, uint64, bool) {
		return 0, 0, false
	}
	mockIsAlive := func(target processTarget) bool {
		return isTargetAlive(target, mockStat)
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, mockStat, 10*time.Millisecond, 2*time.Second)

	// Since stat read failed, 600 was not tracked and not killed
	g.Expect(killed).To(BeEmpty())
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

	mockKill := func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		switch target.pid {
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

	mockIsAlive := func(target processTarget) bool {
		return false
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)

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

	mockKill := func(target processTarget) error {
		return nil
	}

	mockIsAlive := func(target processTarget) bool {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 20*time.Millisecond, 5*time.Second)
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

	mockKill := func(target processTarget) error {
		return nil
	}

	mockIsAlive := func(target processTarget) bool {
		return true
	}

	ctx := context.Background()
	start := time.Now()
	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 20*time.Millisecond, 60*time.Millisecond)
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
	mockKill := func(target processTarget) error {
		return nil
	}
	mockIsAlive := func(target processTarget) bool {
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	removeContainers(ctx, mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)
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
	mockKill := func(target processTarget) error {
		t.Fatalf("kill should not be called")
		return nil
	}
	mockIsAlive := func(target processTarget) bool {
		return false
	}

	start := time.Now()
	removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 100*time.Millisecond, 2*time.Second)
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(1))
	g.Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond))
}

func TestRemoveContainers_GetPIDsErrorInitial(t *testing.T) {
	g := NewWithT(t)

	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		return nil, errors.New("ps failed")
	}
	mockKill := func(target processTarget) error {
		t.Fatalf("kill should not be called")
		return nil
	}
	mockIsAlive := func(target processTarget) bool {
		return false
	}

	removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)
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
	mockKill := func(target processTarget) error {
		killed = append(killed, target.pid)
		return nil
	}
	mockIsAlive := func(target processTarget) bool {
		return false
	}

	removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)
	g.Expect(killed).To(Equal([]int{600}))
}

func TestRemoveContainers_PublicEntryPoint(t *testing.T) {
	g := NewWithT(t)

	origGetPIDs := runningContainerdShimPIDs
	origKill := killProcessTarget
	origIsAlive := isProcessAlive
	origStat := readProcStat
	origInterval := containersPollInterval
	origTimeout := containersMaxWaitTimeout

	t.Cleanup(func() {
		runningContainerdShimPIDs = origGetPIDs
		killProcessTarget = origKill
		isProcessAlive = origIsAlive
		readProcStat = origStat
		containersPollInterval = origInterval
		containersMaxWaitTimeout = origTimeout
	})

	var mu sync.Mutex
	var calls int
	var killed []int

	runningContainerdShimPIDs = func(ctx context.Context, strict bool) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []string{"700"}, nil
		}
		return nil, nil
	}

	killProcessTarget = func(target processTarget) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, target.pid)
		return nil
	}

	isProcessAlive = func(target processTarget) bool {
		return false
	}

	readProcStat = defaultMockStat

	containersPollInterval = 10 * time.Millisecond
	containersMaxWaitTimeout = 1 * time.Second

	RemoveContainers(context.Background(), false)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(2))
	g.Expect(killed).To(Equal([]int{700}))
}

func TestDefaultIsProcessAliveAndStat(t *testing.T) {
	g := NewWithT(t)

	// Current process is alive
	selfPid := os.Getpid()
	state, startTime, exists := defaultReadProcessStat(selfPid)
	g.Expect(exists).To(BeTrue())
	g.Expect(state).To(Not(Equal(byte('Z'))))
	g.Expect(startTime).To(BeNumerically(">", 0))

	target := processTarget{pid: selfPid, startTime: startTime}
	g.Expect(defaultIsProcessAlive(target)).To(BeTrue())

	// If start time does not match, treated as dead (recycled PID)
	reusedTarget := processTarget{pid: selfPid, startTime: startTime + 9999}
	g.Expect(defaultIsProcessAlive(reusedTarget)).To(BeFalse())

	// Non-existent PID
	nonExistentTarget := processTarget{pid: 4194300, startTime: 100}
	g.Expect(defaultIsProcessAlive(nonExistentTarget)).To(BeFalse())
}

func TestDefaultKillProcessTarget_RecycledPIDSkipped(t *testing.T) {
	g := NewWithT(t)

	// Target has mismatched start time -> defaultKillProcessTarget should return nil without error
	selfPid := os.Getpid()
	target := processTarget{pid: selfPid, startTime: 999999999}
	err := defaultKillProcessTarget(target)
	g.Expect(err).ToNot(HaveOccurred())
}

func TestRemoveContainers_ReturnsNetworkNamespaces(t *testing.T) {
	g := NewWithT(t)

	var calls int
	mockGetPIDs := func(ctx context.Context) ([]string, error) {
		calls++
		if calls == 1 {
			// The test process stands in for a container process; kill is mocked.
			return []string{strconv.Itoa(os.Getpid())}, nil
		}
		return nil, nil
	}
	mockKill := func(target processTarget) error { return nil }
	mockIsAlive := func(target processTarget) bool { return false }

	netns := removeContainers(context.Background(), mockGetPIDs, mockKill, mockIsAlive, defaultMockStat, 10*time.Millisecond, 2*time.Second)

	g.Expect(netns).To(HaveLen(1))
	g.Expect(netns.contains("/proc/self/ns/net")).To(BeTrue())
}
