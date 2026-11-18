package internal

import (
	"context"
	"errors"
	"os"
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
			// Child process spawned or discovered on second pass
			return []string{"102"}, nil
		default:
			// 0 PIDs on pass 3 -> loop terminates immediately
			return nil, nil
		}
	}

	mockKill := func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		return nil
	}

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, 10*time.Millisecond, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(3))
	g.Expect(killed).To(Equal([]int{100, 101, 102}))
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

	ctx := context.Background()
	removeContainers(ctx, mockGetPIDs, mockKill, 10*time.Millisecond, 2*time.Second)

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
		// Always return running shims so loop would run forever without timeout
		return []string{"300"}, nil
	}

	mockKill := func(pid int) error {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	removeContainers(ctx, mockGetPIDs, mockKill, 20*time.Millisecond, 5*time.Second)
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

	ctx := context.Background() // no deadline on parent ctx
	start := time.Now()
	// Bounded by maxWaitTimeout = 60ms
	removeContainers(ctx, mockGetPIDs, mockKill, 20*time.Millisecond, 60*time.Millisecond)
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel beforehand

	removeContainers(ctx, mockGetPIDs, mockKill, 10*time.Millisecond, 2*time.Second)
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

	start := time.Now()
	removeContainers(context.Background(), mockGetPIDs, mockKill, 100*time.Millisecond, 2*time.Second)
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

	removeContainers(context.Background(), mockGetPIDs, mockKill, 10*time.Millisecond, 2*time.Second)
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

	removeContainers(context.Background(), mockGetPIDs, mockKill, 10*time.Millisecond, 2*time.Second)
	g.Expect(killed).To(Equal([]int{600}))
}

func TestRemoveContainers_PublicEntryPoint(t *testing.T) {
	g := NewWithT(t)

	origGetPIDs := runningContainerdShimPIDs
	origKill := killProcessPID
	origInterval := containersPollInterval
	origTimeout := containersMaxWaitTimeout

	t.Cleanup(func() {
		runningContainerdShimPIDs = origGetPIDs
		killProcessPID = origKill
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

	containersPollInterval = 10 * time.Millisecond
	containersMaxWaitTimeout = 1 * time.Second

	RemoveContainers(context.Background())

	mu.Lock()
	defer mu.Unlock()
	g.Expect(calls).To(Equal(2))
	g.Expect(killed).To(Equal([]int{700}))
}
