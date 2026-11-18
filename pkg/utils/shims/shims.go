package shims

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// detachedSnapShimExe is the /proc/<pid>/exe path after snapd lazy-unmounts an old revision.
const detachedSnapShimExe = "/bin/containerd-shim-runc-v2"

// reBinaryCmd matches containerd-shim-runc-v2 command lines for k8s and k8s_<instance> snaps.
var reBinaryCmd = regexp.MustCompile(`^(/var/lib/snapd)?/snap/k8s(_[a-zA-Z0-9-]+)?/.*/bin/containerd-shim-runc-v2($|\s)`)

// reBinaryExe matches the exact containerd-shim-runc-v2 executable path for k8s and k8s_<instance> snaps.
var reBinaryExe = regexp.MustCompile(`^(/var/lib/snapd)?/snap/k8s(_[a-zA-Z0-9-]+)?/.*/bin/containerd-shim-runc-v2$`)

var reBinaryName = reBinaryCmd

var (
	osReadlink    = os.Readlink
	osStat        = os.Stat
	statfsType    = unixStatfsType
	listProcesses = listAllSystemProcesses
)

// RunningContainerdShimPIDs returns a list of all the pids on the system that have been started by a containerd shim.
func RunningContainerdShimPIDs(ctx context.Context) ([]string, error) {
	procs, err := listProcesses(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list running host processes: %w", err)
	}

	return findAllChildren(findShimPIDs(procs), makeShallowChildPIDs(procs)), nil
}

type processInfo struct {
	command   string
	parentPID string
}

// listAllSystemProcesses returns a map of all running processes on the host.
// for each process, we store the ppid and the command line.
func listAllSystemProcesses(ctx context.Context) (map[string]processInfo, error) {
	// output is a list of lines in the following format, one line for each running process:
	// [pid] [ppid] [arg1 arg2 arg3 ...]
	// [pid] [ppid] [arg1 arg2 arg3 ...]
	stdout, err := exec.CommandContext(ctx, "ps", "-e", "-o", "pid=,ppid=,args=").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute ps command (output=%q): %w", stdout, err)
	}

	return parseProcessList(string(stdout)), nil
}

// parseProcessList parses `ps -e -o pid=,ppid=,args=` output into a PID-keyed map.
func parseProcessList(output string) map[string]processInfo {
	result := map[string]processInfo{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		result[parts[0]] = processInfo{
			parentPID: parts[1],
			command:   strings.Join(parts[2:], " "),
		}
	}

	return result
}

func sortPIDs(pids []string) {
	slices.SortFunc(pids, func(a, b string) int {
		ia, erra := strconv.Atoi(a)
		ib, errb := strconv.Atoi(b)
		if erra == nil && errb == nil {
			return cmp.Compare(ia, ib)
		}
		return strings.Compare(a, b)
	})
}

// makeShallowChildPIDs returns a shallow map of the direct children for each process.
func makeShallowChildPIDs(procs map[string]processInfo) map[string][]string {
	result := make(map[string][]string, len(procs))
	pids := make([]string, 0, len(procs))
	for pid := range procs {
		pids = append(pids, pid)
	}
	sortPIDs(pids)
	for _, pid := range pids {
		info := procs[pid]
		result[info.parentPID] = append(result[info.parentPID], pid)
	}
	return result
}

// findShimPIDs returns the list of PIDs of the parent shim processes.
func findShimPIDs(procs map[string]processInfo) []string {
	return findShimPIDsWithReadlink(procs, osReadlink)
}

func findShimPIDsWithReadlink(procs map[string]processInfo, readlink func(string) (string, error)) []string {
	if readlink == nil {
		readlink = osReadlink
	}

	pids := make([]string, 0, len(procs))
	for pid := range procs {
		pids = append(pids, pid)
	}
	sortPIDs(pids)

	var result []string
	for _, pid := range pids {
		info := procs[pid]
		if !reBinaryCmd.MatchString(info.command) && !strings.Contains(info.command, "containerd-shim-runc-v2") {
			continue
		}

		procExePath := fmt.Sprintf("/proc/%s/exe", pid)
		exe, err := readlink(procExePath)
		if err != nil {
			continue
		}

		exe = strings.TrimSuffix(exe, " (deleted)")
		if reBinaryExe.MatchString(exe) || (exe == detachedSnapShimExe && reBinaryCmd.MatchString(info.command) && isDetachedSnapExe(procExePath)) {
			result = append(result, pid)
		}
	}
	return result
}

func isDetachedSnapExe(procExePath string) bool {
	procInfo, err := osStat(procExePath)
	if err != nil {
		return false
	}
	if hostInfo, err := osStat(detachedSnapShimExe); err == nil && os.SameFile(procInfo, hostInfo) {
		return false
	}
	fsType, err := statfsType(procExePath)
	if err != nil {
		return false
	}
	return fsType == unix.SQUASHFS_MAGIC || fsType == unix.FUSE_SUPER_MAGIC
}

func unixStatfsType(path string) (int64, error) {
	var sfs unix.Statfs_t
	if err := unix.Statfs(path, &sfs); err != nil {
		return 0, err
	}
	return int64(sfs.Type), nil
}

// findAllChildren returns a list of all process IDs starting from a given set of parents in post-order (children before parent shim).
func findAllChildren(startPIDs []string, shallowChildPIDs map[string][]string) []string {
	var result []string
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var visit func(pid string)
	visit = func(pid string) {
		if pid == "" || visited[pid] || inStack[pid] {
			return
		}
		inStack[pid] = true
		for _, child := range shallowChildPIDs[pid] {
			visit(child)
		}
		inStack[pid] = false
		visited[pid] = true
		result = append(result, pid)
	}

	for _, pid := range startPIDs {
		visit(pid)
	}
	return result
}
