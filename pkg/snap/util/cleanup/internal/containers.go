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

// RemoveContainers kills every process in the k8s snap's containerd shim trees and waits for
// them to exit, so that the network and mount cleanup that follows does not race them.
// It returns the network namespaces the processes were running in.
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

	return killProcesses(ctx, intPids, containerExitTimeout)
}

// killProcesses SIGKILLs the given processes, waits up to timeout for them to exit, and returns
// their network namespaces. Processes are signalled and waited for through pidfds, which keep
// referring to the same process even after it exits and its PID is reused.
func killProcesses(ctx context.Context, pids []int, timeout time.Duration) NetworkNamespaces {
	log := log.FromContext(ctx)
	netns := NetworkNamespaces{}

	var pidfds []int
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
		pidfds = append(pidfds, fd)
		netns.add(fmt.Sprintf("/proc/%d/ns/net", pid))

		if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			log.Error(err, "failed to kill container process", "pid", pid)
		}
	}

	deadline := time.Now().Add(timeout)
	running := 0
	for _, fd := range pidfds {
		if !waitForExit(fd, deadline) {
			running++
		}
	}
	if running > 0 {
		log.Info("Container processes did not exit in time, continuing cleanup", "running", running, "timeout", timeout)
	}
	return netns
}

// waitForExit reports whether the process behind pidfd exited before deadline.
// A pidfd becomes readable once its process has exited.
func waitForExit(pidfd int, deadline time.Time) bool {
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(max(time.Until(deadline), 0).Milliseconds()))
		if !errors.Is(err, unix.EINTR) {
			return n > 0
		}
	}
}
