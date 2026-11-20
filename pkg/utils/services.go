package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"gopkg.in/yaml.v3"
)

// ServiceArgsFromMap processes a map of string pointers and categorizes them into update and delete lists.
// - If the value pointer is nil, it adds the argument name to the delete list.
// - If the value pointer is not nil, it adds the argument and its value to the update map.
func ServiceArgsFromMap(args map[string]*string) (map[string]string, []string) {
	updateArgs := make(map[string]string)
	deleteArgs := make([]string, 0)

	for arg, val := range args {
		if val == nil {
			deleteArgs = append(deleteArgs, arg)
		} else {
			updateArgs[arg] = *val
		}
	}
	return updateArgs, deleteArgs
}

var (
	ErrUnitNotRunning = errors.New("unit is not running")
	errUnitNotFound   = errors.New("unit was not found")

	procDir            = "/proc"
	expectedServiceUID = uint32(0)

	osReadlinkService = os.Readlink
	systemctlUsable   = func() *atomic.Bool {
		var b atomic.Bool
		b.Store(true)
		return &b
	}()
)

func isStrictSnapEnv() bool {
	snapEnv := os.Getenv("SNAP")
	if snapEnv == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(snapEnv, "meta", "snap.yaml"))
	if err != nil {
		return false
	}
	var meta struct {
		Name        string `yaml:"name"`
		Confinement string `yaml:"confinement"`
	}
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return false
	}
	return meta.Name == "k8s" && meta.Confinement == "strict"
}

// RunningServiceArgs queries systemd for the MainPID of the snap service unit
// and returns its parsed command-line arguments from /proc/<pid>/cmdline.
// Returns nil, nil when the service is not running.
func RunningServiceArgs(ctx context.Context, serviceName string) (map[string]string, error) {
	snapInst := os.Getenv("SNAP_INSTANCE_NAME")
	if snapInst != "k8s" && !strings.HasPrefix(snapInst, "k8s_") {
		snapInst = "k8s"
	}
	unitName := fmt.Sprintf("snap.%s.%s.service", snapInst, serviceName)
	pid, err := runningServiceMainPID(ctx, unitName, serviceName)
	if err != nil {
		return nil, err
	}
	cmdlineData, err := os.ReadFile(filepath.Join(procDir, fmt.Sprintf("%d", pid), "cmdline"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// this branch is the similar to pid == 0. it shouldn't really happen, unless due to a race condition.
			return nil, fmt.Errorf("cmdline file doesn't exist for unit %q (pid %d): %w", unitName, pid, ErrUnitNotRunning)
		}
		return nil, fmt.Errorf("failed to read cmdline for pid %d (unit %q): %w", pid, unitName, err)
	}

	args := make(map[string]string)
	for _, part := range bytes.Split(cmdlineData, []byte{0})[1:] { // skip argv[0] (binary path)
		if len(part) == 0 {
			continue
		}
		key, value := ParseArgumentLine(string(part))
		if key != "" {
			args[key] = value
		}
	}
	return args, nil
}

// runningServiceMainPID returns the MainPID of a running snap service unit.
// It queries systemd where that is usable (classic confinement) and falls
// back to locating the service process by argv[0] in /proc, because
// systemctl is not usable from a strictly confined snap.
func runningServiceMainPID(ctx context.Context, unitName string, serviceName string) (int, error) {
	if !isStrictSnapEnv() && systemctlUsable.Load() {
		pid, err := runningServiceMainPIDSystemd(ctx, unitName)
		if err == nil {
			return pid, nil
		}
		if errors.Is(err, ErrUnitNotRunning) || errors.Is(err, errUnitNotFound) || ctx.Err() != nil {
			return 0, err
		}
		systemctlUsable.Store(false)
	}

	return findServicePID(serviceName)
}

// runningServiceMainPIDSystemd returns the MainPID of a running snap service
// unit by querying systemd.
func runningServiceMainPIDSystemd(ctx context.Context, unitName string) (int, error) {
	loadState, err := getUnitLoadState(ctx, unitName)
	if err != nil {
		return 0, fmt.Errorf("failed to get unit %q LoadState: %w", unitName, err)
	}

	if loadState == LoadStateNotFound {
		return 0, fmt.Errorf("unit %q: %w", unitName, errUnitNotFound)
	}

	activeState, err := getUnitActiveState(ctx, unitName)
	if err != nil {
		return 0, fmt.Errorf("failed to get unit %q ActiveState: %w", unitName, err)
	}

	if activeState == ActiveStateInactive {
		return 0, fmt.Errorf("%q: %w", unitName, ErrUnitNotRunning)
	}

	pid, err := getUnitMainPID(ctx, unitName)
	if err != nil {
		return 0, fmt.Errorf("failed to get unit %q MainPID: %w", unitName, err)
	}

	if pid == 0 {
		return 0, fmt.Errorf("pid is 0 for unit %q: %w", unitName, ErrUnitNotRunning)
	}

	return pid, nil
}

// isZombieProcess checks if the process state is zombie (Z) or dead (X).
func isZombieProcess(procPath string, pid string) bool {
	statData, err := os.ReadFile(filepath.Join(procPath, pid, "stat"))
	if err != nil {
		return false
	}
	idx := bytes.LastIndexByte(statData, ')')
	if idx == -1 || idx+2 >= len(statData) {
		return false
	}
	state := statData[idx+2]
	return state == 'Z' || state == 'X' || state == 'x'
}

// isSnapServiceBinary checks if binPath is a clean path to the expected snap service binary.
func isSnapServiceBinary(binPath string, serviceName string) bool {
	if binPath == "" || serviceName == "" || strings.Contains(serviceName, "/") || filepath.Clean(binPath) != binPath {
		return false
	}
	if snapEnv := os.Getenv("SNAP"); snapEnv != "" && binPath == filepath.Join(filepath.Clean(snapEnv), "bin", serviceName) {
		return true
	}

	suffix := "/bin/" + serviceName
	if !strings.HasSuffix(binPath, suffix) {
		return false
	}
	cleaned := strings.TrimPrefix(strings.TrimSuffix(binPath, suffix), "/var/lib/snapd")
	if !strings.HasPrefix(cleaned, "/snap/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/snap/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	inst := parts[0]
	if snapInst := os.Getenv("SNAP_INSTANCE_NAME"); snapInst == "k8s" || strings.HasPrefix(snapInst, "k8s_") {
		return inst == snapInst
	}
	return inst == "k8s" || strings.HasPrefix(inst, "k8s_")
}

// isKubernetesMulticallService reports whether serviceName is shipped as a
// symlink to the multicall bin/kubernetes binary in the k8s snap.
func isKubernetesMulticallService(serviceName string) bool {
	switch serviceName {
	case "kubelet", "kube-apiserver", "kube-controller-manager", "kube-scheduler", "kube-proxy":
		return true
	default:
		return false
	}
}

func isSnapServiceExecutable(exePath string, serviceName string) bool {
	if isSnapServiceBinary(exePath, serviceName) {
		return true
	}
	return isKubernetesMulticallService(serviceName) && isSnapServiceBinary(exePath, "kubernetes")
}

func isDetachedServiceExecutable(exePath string, serviceName string) bool {
	if exePath == "/bin/"+serviceName {
		return true
	}
	return isKubernetesMulticallService(serviceName) && exePath == "/bin/kubernetes"
}

// hasSnapServiceProfile checks whether /proc/<pid>/attr/current matches snap.<instance>.<serviceName>.
func hasSnapServiceProfile(pidDir string, serviceName string) bool {
	if serviceName == "" || strings.Contains(serviceName, "/") {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(pidDir, "attr", "current"))
	if err != nil {
		raw, err = os.ReadFile(filepath.Join(pidDir, "attr", "apparmor", "current"))
		if err != nil {
			return false
		}
	}
	label := strings.TrimSpace(string(bytes.TrimRight(raw, "\x00\r\n")))
	if idx := strings.IndexByte(label, ' '); idx >= 0 {
		label = label[:idx]
	}
	if idx := strings.LastIndexByte(label, ':'); idx >= 0 {
		label = label[idx+1:]
	}
	if !strings.HasPrefix(label, "snap.") {
		return false
	}
	suffix := "." + serviceName
	if !strings.HasSuffix(label, suffix) {
		return false
	}
	inst := strings.TrimSuffix(strings.TrimPrefix(label, "snap."), suffix)
	if snapInst := os.Getenv("SNAP_INSTANCE_NAME"); snapInst == "k8s" || strings.HasPrefix(snapInst, "k8s_") {
		return inst == snapInst
	}
	return inst == "k8s" || strings.HasPrefix(inst, "k8s_")
}

// findServicePID returns the pid of the root-owned snap service whose argv[0]
// and /proc/<pid>/exe point at the snap's bin/<serviceName> binary.
func findServicePID(serviceName string) (int, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", procDir, err)
	}

	var rejectedDiag string
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		pidDir := filepath.Join(procDir, entry.Name())
		info, err := os.Stat(pidDir)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != expectedServiceUID {
			continue
		}

		if isZombieProcess(procDir, entry.Name()) {
			continue
		}

		cmdline, err := os.ReadFile(filepath.Join(pidDir, "cmdline"))
		if err != nil {
			continue
		}
		argv0, _, _ := bytes.Cut(cmdline, []byte{0})
		if !isSnapServiceBinary(string(argv0), serviceName) {
			continue
		}

		exe, err := osReadlinkService(filepath.Join(pidDir, "exe"))
		if err == nil {
			exe = strings.TrimSuffix(exe, " (deleted)")
			if isSnapServiceExecutable(exe, serviceName) {
				return pid, nil
			}
		}
		if (err != nil || isDetachedServiceExecutable(exe, serviceName)) && hasSnapServiceProfile(pidDir, serviceName) {
			return pid, nil
		}
		rejectedDiag = fmt.Sprintf(" (candidate pid %d rejected: exe=%q err=%v)", pid, exe, err)
	}

	return 0, fmt.Errorf("no running process found for service %q%s: %w", serviceName, rejectedDiag, ErrUnitNotRunning)
}

type LoadState string

const (
	LoadStateLoaded   = "loaded"
	LoadStateNotFound = "not-found"
)

func getUnitLoadState(ctx context.Context, unit string) (LoadState, error) {
	out, err := HostCommandContext(ctx, "systemctl", "show", unit, "--property=LoadState", "--value").Output()
	if err != nil {
		return "", fmt.Errorf("failed to query LoadState for unit %q: %w", unit, err)
	}

	switch st := strings.TrimSpace(string(out)); st {
	case "loaded":
		return LoadStateLoaded, nil
	case "not-found":
		return LoadStateNotFound, nil
	default:
		return "", fmt.Errorf("invalid LoadState %q for unit %q", st, unit)
	}
}

type ActiveState string

const (
	ActiveStateActive   ActiveState = "active"
	ActiveStateInactive ActiveState = "inactive"
)

func getUnitActiveState(ctx context.Context, unit string) (ActiveState, error) {
	out, err := HostCommandContext(ctx, "systemctl", "show", unit, "--property=ActiveState", "--value").Output()
	if err != nil {
		return "", fmt.Errorf("failed to query ActiveState for unit %q: %w", unit, err)
	}

	switch st := strings.TrimSpace(string(out)); st {
	case "active":
		return ActiveStateActive, nil
	case "inactive":
		return ActiveStateInactive, nil
	default:
		return "", fmt.Errorf("invalid ActiveState %q for unit %q", st, unit)
	}
}

func getUnitMainPID(ctx context.Context, unit string) (int, error) {
	out, err := HostCommandContext(ctx, "systemctl", "show", unit, "--property=MainPID", "--value").Output()
	if err != nil {
		return 0, fmt.Errorf("failed to query MainPID for unit %q: %w", unit, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("failed to convert unit %q pid %q to int: %w", unit, string(out), err)
	}

	return pid, nil
}
