package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils/shims"
)

var (
	runningContainerdShimPIDs = shims.RunningContainerdShimPIDs
	killProcessPID            = defaultKillProcessPID
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

func RemoveContainers(ctx context.Context) {
	removeContainers(ctx, runningContainerdShimPIDs, killProcessPID, containersPollInterval, containersMaxWaitTimeout)
}

func removeContainers(
	ctx context.Context,
	getPIDs func(context.Context) ([]string, error),
	killPID func(int) error,
	pollInterval time.Duration,
	maxWaitTimeout time.Duration,
) {
	log := log.FromContext(ctx)

	waitCtx, cancel := context.WithTimeout(ctx, maxWaitTimeout)
	defer cancel()

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

		if len(pids) == 0 {
			return
		}

		for _, pid := range pids {
			intPid, err := strconv.Atoi(pid)
			if err != nil {
				log.Error(err, "failed to convert PID to integer", "pid", pid)
				continue
			}

			if err := killPID(intPid); err != nil {
				if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
					log.Error(err, "failed to kill containerd shim PID", "pid", intPid)
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
	}
}
