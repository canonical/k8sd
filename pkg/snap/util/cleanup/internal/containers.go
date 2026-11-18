package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils/shims"
)

type processTarget struct {
	pid       int
	startTime uint64
}

var (
	runningContainerdShimPIDs = shims.RunningContainerdShimPIDs
	killProcessTarget         = defaultKillProcessTarget
	isProcessAlive            = defaultIsProcessAlive
	readProcStat              = defaultReadProcessStat
	containersPollInterval    = 100 * time.Millisecond
	containersMaxWaitTimeout  = 5 * time.Second
)

func defaultReadProcessStat(pid int) (state byte, startTime uint64, exists bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, false
	}
	content := string(data)
	idx := strings.LastIndexByte(content, ')')
	if idx == -1 || idx+1 >= len(content) {
		return 0, 0, false
	}
	fields := strings.Fields(content[idx+1:])
	if len(fields) < 20 {
		return 0, 0, false
	}
	state = fields[0][0]
	st, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return state, 0, true
	}
	return state, st, true
}

func defaultIsProcessAlive(target processTarget) bool {
	state, startTime, exists := readProcStat(target.pid)
	if !exists {
		return false
	}
	// A zombie ('Z') or dead ('X') process has exited and released all namespaces/mounts.
	if state == 'Z' || state == 'X' {
		return false
	}
	// If the start time has changed, the PID has been recycled by another process.
	if target.startTime != 0 && startTime != 0 && startTime != target.startTime {
		return false
	}
	return true
}

func defaultKillProcessTarget(target processTarget) error {
	if !isProcessAlive(target) {
		return nil
	}
	proc, err := os.FindProcess(target.pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}
	return proc.Kill()
}

func RemoveContainers(ctx context.Context) {
	removeContainers(
		ctx,
		runningContainerdShimPIDs,
		killProcessTarget,
		isProcessAlive,
		readProcStat,
		containersPollInterval,
		containersMaxWaitTimeout,
	)
}

func removeContainers(
	ctx context.Context,
	getPIDs func(context.Context) ([]string, error),
	killTarget func(processTarget) error,
	isAlive func(processTarget) bool,
	getStat func(int) (byte, uint64, bool),
	pollInterval time.Duration,
	maxWaitTimeout time.Duration,
) {
	log := log.FromContext(ctx)

	waitCtx, cancel := context.WithTimeout(ctx, maxWaitTimeout)
	defer cancel()

	trackedTargets := make(map[int]processTarget)
	var order []int

	for {
		if waitCtx.Err() != nil {
			return
		}

		pids, err := getPIDs(waitCtx)
		if err != nil {
			if waitCtx.Err() == nil {
				log.Error(err, "failed to get containerd shim PIDs")
			}
			if len(trackedTargets) == 0 {
				return
			}
		} else {
			// pids are returned in post-order: children before parent shim.
			// Prepend newly discovered PIDs to preserve post-order so children are killed before parent shims.
			var newPIDs []int
			for _, pidStr := range pids {
				intPid, err := strconv.Atoi(pidStr)
				if err != nil {
					log.Error(err, "failed to convert PID to integer", "pid", pidStr)
					continue
				}
				if _, ok := trackedTargets[intPid]; !ok {
					_, startTime, _ := getStat(intPid)
					trackedTargets[intPid] = processTarget{pid: intPid, startTime: startTime}
					newPIDs = append(newPIDs, intPid)
				}
			}
			order = append(newPIDs, order...)
		}

		if len(pids) == 0 && len(trackedTargets) == 0 {
			return
		}

		// Kill in post-order (children before parent shims)
		for _, pid := range order {
			target, ok := trackedTargets[pid]
			if !ok {
				continue
			}
			if err := killTarget(target); err != nil {
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
		var remainingOrder []int
		for _, pid := range order {
			target, ok := trackedTargets[pid]
			if !ok {
				continue
			}
			if isAlive(target) {
				remainingOrder = append(remainingOrder, pid)
			} else {
				delete(trackedTargets, pid)
			}
		}
		order = remainingOrder
	}
}
