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
	"syscall"
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

	procDir = "/proc"
)

// RunningServiceArgs queries systemd for the MainPID of the snap service unit
// and returns its parsed command-line arguments from /proc/<pid>/cmdline.
// Returns nil, nil when the service is not running.
// Under strict confinement systemd is unreachable, so the process is looked up in /proc instead.
func RunningServiceArgs(ctx context.Context, serviceName string, strict bool) (map[string]string, error) {
	unitName := fmt.Sprintf("snap.k8s.%s.service", serviceName)
	if strict {
		pid, err := confinedServicePID(serviceName)
		if err != nil {
			return nil, err
		}
		return processArgs(unitName, pid)
	}

	// unit not found:
	// LoadState=not-found
	// ActiveState=inactive

	// unit not running
	// LoadState=loaded
	// ActiveState=inactive

	// unit running
	// LoadState=loaded
	// ActiveState=active

	loadState, err := getUnitLoadState(ctx, unitName)
	if err != nil {
		return nil, fmt.Errorf("failed to get unit %q LoadState: %w", unitName, err)
	}

	if loadState == LoadStateNotFound {
		return nil, fmt.Errorf("unit %q was not found", unitName)
	}

	// unit is loaded, let's see if it's running

	activeState, err := getUnitActiveState(ctx, unitName)
	if err != nil {
		return nil, fmt.Errorf("failed to get unit %q ActiveState: %w", unitName, err)
	}

	if activeState == ActiveStateInactive {
		return nil, fmt.Errorf("%q: %w", unitName, ErrUnitNotRunning)
	}

	// unit is loaded and running, let's get its pid

	pid, err := getUnitMainPID(ctx, unitName)
	if err != nil {
		return nil, fmt.Errorf("failed to get unit %q MainPID: %w", unitName, err)
	}

	if pid == 0 {
		// this should only happen in case of a race condition.
		// for snap services with Type=simple or Type=notify (containerd),
		//  MainPID is only 0 if the unit is either inactive or not-found.
		// since we're making sure the service is loaded and active before getting the PID,
		// the only possible explanation for this branch is that the service was stopped right after
		// we checked the load and active states.
		return nil, fmt.Errorf("pid is 0 for unit %q: %w", unitName, ErrUnitNotRunning)
	}
	return processArgs(unitName, pid)
}

func processArgs(unitName string, pid int) (map[string]string, error) {
	cmdlineData, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "cmdline"))
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

// confinedServicePID returns the pid of the snap service process running under
// its snap.k8s.<service> AppArmor profile.
func confinedServicePID(serviceName string) (int, error) {
	label := "snap.k8s." + serviceName
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", procDir, err)
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		pidDir := filepath.Join(procDir, entry.Name())
		if ProcessAppArmorLabel(pidDir) != label {
			continue
		}
		// Child processes inherit the service profile, so also match the service binary.
		cmdline, err := os.ReadFile(filepath.Join(pidDir, "cmdline"))
		if err != nil {
			continue
		}
		argv0, _, _ := bytes.Cut(cmdline, []byte{0})
		if filepath.Base(string(argv0)) == serviceName {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("no process confined by %q: %w", label, ErrUnitNotRunning)
}

// ProcessAppArmorLabel returns the AppArmor label of the process at pidDir, or ""
// if it cannot be read. Only processes of the current user are inspected, as reading
// another user's label needs CAP_SYS_PTRACE, which strict confinement denies.
func ProcessAppArmorLabel(pidDir string) string {
	info, err := os.Stat(pidDir)
	if err != nil {
		return ""
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(pidDir, "attr", "current"))
	if err != nil {
		return ""
	}
	label, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	return label
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
