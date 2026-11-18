package internal

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils/shims"
	"golang.org/x/sys/unix"
)

// containerExitTimeout bounds how long RemoveContainers waits for killed processes to exit.
const containerExitTimeout = 10 * time.Second

// RemoveContainers kills every process in the k8s snap's containerd shim trees, waits for them
// to exit, and returns the network namespaces they were running in.
func RemoveContainers(ctx context.Context) NetworkNamespaces {
	log := log.FromContext(ctx)
	pids, err := shims.RunningContainerdShimPIDs(ctx)
	if err != nil {
		log.Error(err, "failed to get containerd shim PIDs")
		return nil
	}

	intPids := make([]int, 0, len(pids))
	for _, pid := range pids {
		intPid, err := strconv.Atoi(pid)
		if err != nil {
			log.Error(err, "failed to convert PID to integer", "pid", pid)
			continue
		}
		intPids = append(intPids, intPid)
	}

	return killProcesses(ctx, intPids, shims.IsSnapOwned, containerExitTimeout)
}

// killProcesses SIGKILLs the given processes that isOwned accepts, waits up to timeout for them
// to exit, and returns their network namespaces.
//
// Each process is pinned with a pidfd before isOwned checks it, so a PID that was recycled since
// it was listed is never signalled. All processes are pinned before any is signalled, so the
// ancestors isOwned walks through are still alive.
func killProcesses(ctx context.Context, pids []int, isOwned func(pid int) bool, timeout time.Duration) NetworkNamespaces {
	log := log.FromContext(ctx)
	owned := NetworkNamespaces{}

	pidfds := make([]int, 0, len(pids))
	defer func() {
		for _, fd := range pidfds {
			_ = unix.Close(fd)
		}
	}()

	for _, pid := range pids {
		fd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			if !errors.Is(err, unix.ESRCH) {
				log.Error(err, "failed to open pidfd", "pid", pid)
			}
			continue
		}
		if !isOwned(pid) {
			_ = unix.Close(fd)
			continue
		}
		owned.add(fmt.Sprintf("/proc/%d/ns/net", pid))
		pidfds = append(pidfds, fd)
	}

	for _, fd := range pidfds {
		if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			log.Error(err, "failed to kill container process")
		}
	}

	if err := waitForExit(ctx, pidfds, timeout); err != nil {
		log.Error(err, "container processes did not exit, continuing cleanup")
	}

	return owned
}

// waitForExit blocks until every process referred to by pidfds has exited, timeout elapses,
// or ctx is done. A pidfd becomes readable once its process has exited.
func waitForExit(ctx context.Context, pidfds []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	pending := make([]unix.PollFd, 0, len(pidfds))
	for _, fd := range pidfds {
		pending = append(pending, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
	}

	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%d processes still running after %s", len(pending), timeout)
		}

		// Poll in short slices so that context cancellation is noticed promptly.
		if _, err := unix.Poll(pending, int(min(remaining, 100*time.Millisecond).Milliseconds())); err != nil && !errors.Is(err, unix.EINTR) {
			return fmt.Errorf("failed to poll pidfds: %w", err)
		}

		running := pending[:0]
		for _, p := range pending {
			if p.Revents == 0 {
				running = append(running, p)
			}
		}
		pending = running
	}
	return nil
}
