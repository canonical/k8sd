package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/k8sd/pkg/utils/shims"
	"golang.org/x/sys/unix"
)

type containerProcess struct {
	pid       int
	startTime string
}

func signalContainerPID(pid int, startTime string, identity func(int) (string, error)) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		return fmt.Errorf("open pidfd for container PID %d: %w", pid, err)
	}
	defer unix.Close(fd)
	start, idErr := identity(pid)
	if idErr != nil {
		return idErr
	}
	if start != startTime {
		return fmt.Errorf("container PID %d replaced before pidfd signal", pid)
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("kill container PID %d via pidfd: %w", pid, err)
	}
	return nil
}

// RemoveContainers kills snap-owned shim descendants before releasing their
// named network namespaces.
func RemoveContainers(ctx context.Context) (map[string]bool, error) {
	pids, err := shims.RunningContainerdShimPIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list container processes: %w", err)
	}
	processes, identityErr := captureContainerProcesses(pids)
	pidToNamespaces, nsErr := processNetworkNamespaces(processes, "/proc", "/run/netns")
	exitedPIDs, killErr := killContainerProcesses(ctx, processes, signalContainerPID, containerProcessIdentity)

	exitedSet := make(map[int]bool, len(exitedPIDs))
	for _, pid := range exitedPIDs {
		exitedSet[pid] = true
	}

	namespaceToPIDs := make(map[string][]int)
	for pid, namespaces := range pidToNamespaces {
		for _, ns := range namespaces {
			namespaceToPIDs[ns] = append(namespaceToPIDs[ns], pid)
		}
	}

	owned := make(map[string]bool)
	for ns, memberPIDs := range namespaceToPIDs {
		allExited := true
		for _, memberPID := range memberPIDs {
			if !exitedSet[memberPID] {
				allExited = false
				break
			}
		}
		if allExited && len(memberPIDs) > 0 {
			owned[ns] = true
		}
	}
	if err := errors.Join(identityErr, nsErr, killErr); err != nil {
		return nil, err
	}
	return owned, nil
}

func captureContainerProcesses(pids []string) ([]containerProcess, error) {
	var processes []containerProcess
	var errs []error
	for _, pid := range pids {
		n, err := strconv.Atoi(pid)
		if err != nil || n <= 1 {
			errs = append(errs, fmt.Errorf("invalid container PID %q", pid))
			continue
		}
		start, err := containerProcessIdentity(n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if start != "" {
			processes = append(processes, containerProcess{n, start})
		}
	}
	return processes, errors.Join(errs...)
}

func killContainerProcesses(
	ctx context.Context,
	processes []containerProcess,
	kill func(pid int, startTime string, identity func(int) (string, error)) error,
	identity func(int) (string, error),
) ([]int, error) {
	var errs []error
	var killed []containerProcess
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(append(errs, err)...)
		}
		if process.pid <= 1 || process.startTime == "" {
			errs = append(errs, fmt.Errorf("invalid container identity %+v", process))
			continue
		}
		if err := kill(process.pid, process.startTime, identity); err != nil {
			errs = append(errs, err)
			continue
		}
		killed = append(killed, process)
	}
	var exitedPIDs []int
	for _, process := range killed {
		if err := waitContainerExit(ctx, process, identity); err != nil {
			errs = append(errs, err)
		} else {
			exitedPIDs = append(exitedPIDs, process.pid)
		}
	}
	return exitedPIDs, errors.Join(errs...)
}

func waitContainerExit(ctx context.Context, process containerProcess, identity func(int) (string, error)) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		start, err := identity(process.pid)
		if err != nil {
			return fmt.Errorf("check container PID %d: %w", process.pid, err)
		}
		if start == "" {
			return nil
		}
		if start != process.startTime {
			return fmt.Errorf("container PID %d replaced by another process (start time %s != %s)", process.pid, start, process.startTime)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for container PID %d: %w", process.pid, ctx.Err())
		case <-ticker.C:
		}
	}
}

func containerProcessIdentity(pid int) (string, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// comm can contain spaces and parentheses. State follows its final ')'.
	end := strings.LastIndex(string(stat), ") ")
	if end < 0 || len(stat) <= end+2 {
		return "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(stat[end+2:]))
	if len(fields) < 20 {
		return "", fmt.Errorf("invalid process stat fields")
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return "", nil
	}
	return fields[19], nil
}

func processNetworkNamespaces(processes []containerProcess, procDir, netnsDir string) (map[int][]string, error) {
	pidToNamespaces := make(map[int][]string)
	type netnsInfo struct {
		pid  int
		info os.FileInfo
	}
	var namespaces []netnsInfo
	var errs []error
	for _, process := range processes {
		info, err := os.Stat(filepath.Join(procDir, strconv.Itoa(process.pid), "ns", "net"))
		if err != nil {
			errs = append(errs, fmt.Errorf("stat netns for container PID %d: %w", process.pid, err))
			continue
		}
		namespaces = append(namespaces, netnsInfo{pid: process.pid, info: info})
	}
	entries, err := os.ReadDir(netnsDir)
	if errors.Is(err, os.ErrNotExist) {
		return pidToNamespaces, errors.Join(errs...)
	}
	if err != nil {
		return pidToNamespaces, errors.Join(append(errs, err)...)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "cni-") {
			continue
		}
		info, err := os.Stat(filepath.Join(netnsDir, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, ns := range namespaces {
			if os.SameFile(ns.info, info) {
				pidToNamespaces[ns.pid] = append(pidToNamespaces[ns.pid], entry.Name())
			}
		}
	}
	return pidToNamespaces, errors.Join(errs...)
}

func containerNetworkNamespaces(pids []string, procDir, netnsDir string) (map[string]bool, error) {
	var procs []containerProcess
	for _, pid := range pids {
		n, err := strconv.Atoi(pid)
		if err == nil && n > 1 {
			procs = append(procs, containerProcess{pid: n})
		}
	}
	pidToNamespaces, err := processNetworkNamespaces(procs, procDir, netnsDir)
	owned := make(map[string]bool)
	for _, nss := range pidToNamespaces {
		for _, ns := range nss {
			owned[ns] = true
		}
	}
	return owned, err
}
