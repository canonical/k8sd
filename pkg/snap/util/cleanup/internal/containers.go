package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils/shims"
)

var (
	runningContainerdShimPIDs = shims.RunningContainerdShimPIDs
	killProcessPID            = defaultKillProcessPID
	isProcessAlive            = defaultIsProcessAlive
	containersPollInterval    = 100 * time.Millisecond
	containersMaxWaitTimeout  = 5 * time.Second
)

func defaultKillProcessPID(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}
	return proc.Kill()
}

func defaultIsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return false
	}
	// On EPERM, process exists but belongs to another user.
	return true
}

func RemoveContainers(ctx context.Context) {
	removeContainers(
		ctx,
		runningContainerdShimPIDs,
		killProcessPID,
		isProcessAlive,
		containersPollInterval,
		containersMaxWaitTimeout,
	)
}

func removeContainers(
	ctx context.Context,
	getPIDs func(context.Context) ([]string, error),
	killPID func(int) error,
	isAlive func(int) bool,
	pollInterval time.Duration,
	maxWaitTimeout time.Duration,
) {
	log := log.FromContext(ctx)

	waitCtx, cancel := context.WithTimeout(ctx, maxWaitTimeout)
	defer cancel()

	trackedPIDs := make(map[int]struct{})

	for {
		if waitCtx.Err() != nil {
			return
		}

		pids, err := getPIDs(waitCtx)
		if err != nil {
			if waitCtx.Err() == nil {
				log.Error(err, "failed to get containerd shim PIDs")
			}
			return
		}

		for _, pid := range pids {
			intPid, err := strconv.Atoi(pid)
			if err != nil {
				log.Error(err, "failed to convert PID to integer", "pid", pid)
				continue
			}
			trackedPIDs[intPid] = struct{}{}
		}

		if len(pids) == 0 && len(trackedPIDs) == 0 {
			return
		}

		pidsToKill := make([]int, 0, len(trackedPIDs))
		for pid := range trackedPIDs {
			pidsToKill = append(pidsToKill, pid)
		}
		slices.Sort(pidsToKill)

		for _, pid := range pidsToKill {
			if err := killPID(pid); err != nil {
				if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
					log.Error(err, "failed to kill containerd shim PID", "pid", pid)
				}
			}
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		// Prune processes that have exited after the wait.
		for pid := range trackedPIDs {
			if !isAlive(pid) {
				delete(trackedPIDs, pid)
			}
		}
	}
}
