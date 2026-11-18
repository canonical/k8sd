package cilium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/canonical/k8sd/pkg/snap"
	"github.com/moby/sys/mountinfo"
	"golang.org/x/sys/unix"
)

var (
	ciliumCleanupSem = make(chan struct{}, 1)

	defaultCiliumPIDFile          = "/var/run/cilium/cilium.pid"
	defaultCiliumStateDirs        = []string{"/run/cilium", "/var/lib/cilium"}
	defaultProcDir                = "/proc"
	defaultQuiesceRuntimeServices = quiesceSnapRuntimeServices

	errCiliumAgentRunning = errors.New("cilium-agent is still running")
)

func CleanupNetwork(ctx context.Context, snap snap.Snap) error {
	return cleanupNetworkWith(ctx, snap, defaultCiliumPIDFile, defaultCiliumStateDirs, defaultProcDir, defaultQuiesceRuntimeServices, mountinfo.GetMounts)
}

func cleanupNetworkWith(
	ctx context.Context,
	snap snap.Snap,
	pidFile string,
	stateDirs []string,
	procDir string,
	quiesceServices func(context.Context, snap.Snap) (func() error, error),
	readMounts func(mountinfo.FilterFunc) ([]*mountinfo.Info, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return withCiliumCleanupLock(ctx, snap, func() (rerr error) {
		if quiesceServices != nil {
			restore, err := quiesceServices(ctx, snap)
			if err != nil {
				return fmt.Errorf("stop runtime services before Cilium cleanup: %w", err)
			}
			if restore != nil {
				defer func() {
					if restoreErr := restore(); restoreErr != nil {
						rerr = errors.Join(rerr, restoreErr)
					}
				}()
			}
		}
		if err := ensureCiliumAgentStopped(ctx, pidFile, procDir); err != nil {
			return err
		}

		var errs []error
		if err := cleanupCiliumStateWith(
			ctx,
			snap,
			pidFile,
			os.Lstat,
			nil,
			runCiliumCleanup,
			func(ctx context.Context) error {
				return removeCiliumDirectories(ctx, stateDirs, readMounts)
			},
		); err != nil {
			errs = append(errs, err)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return errors.Join(append(errs, ctxErr)...)
			}
		}

		for _, cmd := range []string{"iptables", "ip6tables", "iptables-legacy", "ip6tables-legacy"} {
			if err := ctx.Err(); err != nil {
				errs = append(errs, err)
				break
			}

			out, err := exec.CommandContext(ctx, fmt.Sprintf("%s-save", cmd)).Output()
			if err != nil {
				if errors.Is(err, exec.ErrNotFound) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to read %s rules: %w", cmd, err))
				if ctxErr := ctx.Err(); ctxErr != nil {
					errs = append(errs, ctxErr)
					break
				}
				continue
			}

			var modified bool
			lines := strings.Split(string(out), "\n")
			for i, line := range lines {
				if strings.Contains(strings.ToLower(line), "cilium") {
					lines[i] = ""
					modified = true
				}
			}

			if !modified {
				continue
			}

			restore := exec.CommandContext(ctx, fmt.Sprintf("%s-restore", cmd))
			restore.Stdin = strings.NewReader(strings.Join(lines, "\n"))
			if err := restore.Run(); err != nil {
				if errors.Is(err, exec.ErrNotFound) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to restore %s rules: %w", cmd, err))
				if ctxErr := ctx.Err(); ctxErr != nil {
					errs = append(errs, ctxErr)
					break
				}
				continue
			}
		}

		return errors.Join(errs...)
	})
}

func quiesceSnapRuntimeServices(ctx context.Context, s snap.Snap) (func() error, error) {
	noop := func() error { return nil }
	if os.Getenv("SNAP_CONTEXT") != "" {
		// Inside a snap hook (such as snap/hooks/remove), snapd holds the snap
		// lifecycle change lock with snap services disabled and stopped.
		return noop, nil
	}
	if s == nil {
		return nil, errors.New("snap runtime is required to stop services before Cilium cleanup")
	}
	active, err := s.ActiveServices(ctx, []string{"containerd", "kubelet"})
	if err != nil {
		return nil, err
	}
	if err := s.StopServices(ctx, []string{"kubelet", "containerd"}, "--no-disable"); err != nil {
		return nil, err
	}
	if len(active) == 0 {
		return noop, nil
	}
	return func() error {
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.StartServices(restartCtx, active, "--no-enable"); err != nil {
			return fmt.Errorf("restart runtime services after Cilium cleanup: %w", err)
		}
		return nil
	}, nil
}

func withCiliumCleanupLock(ctx context.Context, s snap.Snap, fn func() error) error {
	select {
	case ciliumCleanupSem <- struct{}{}:
		defer func() { <-ciliumCleanupSem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if s != nil && s.LockFilesDir() != "" {
		if err := os.MkdirAll(s.LockFilesDir(), 0o700); err != nil {
			return fmt.Errorf("prepare Cilium cleanup lock directory: %w", err)
		}
		lockPath := filepath.Join(s.LockFilesDir(), "cilium-cleanup.lock")
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("open Cilium cleanup lock %s: %w", lockPath, err)
		}
		defer f.Close()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
			if err == nil {
				break
			}
			if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
				return fmt.Errorf("lock Cilium cleanup %s: %w", lockPath, err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()
	}

	return fn()
}

func cleanupCiliumState(ctx context.Context, snap snap.Snap) error {
	return withCiliumCleanupLock(ctx, snap, func() (rerr error) {
		if defaultQuiesceRuntimeServices != nil {
			restore, err := defaultQuiesceRuntimeServices(ctx, snap)
			if err != nil {
				return fmt.Errorf("stop runtime services before Cilium cleanup: %w", err)
			}
			if restore != nil {
				defer func() {
					if restoreErr := restore(); restoreErr != nil {
						rerr = errors.Join(rerr, restoreErr)
					}
				}()
			}
		}
		return cleanupCiliumStateWith(
			ctx,
			snap,
			defaultCiliumPIDFile,
			os.Lstat,
			func(ctx context.Context, pf string) error {
				return ensureCiliumAgentStopped(ctx, pf, defaultProcDir)
			},
			runCiliumCleanup,
			func(ctx context.Context) error {
				return removeCiliumDirectories(ctx, defaultCiliumStateDirs, mountinfo.GetMounts)
			},
		)
	})
}

func cleanupCiliumStateWith(
	ctx context.Context,
	snap snap.Snap,
	pidFile string,
	statHelper func(string) (os.FileInfo, error),
	checkStopped func(context.Context, string) error,
	runCleanup func(context.Context, string, string) error,
	removeDirectories func(context.Context) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if checkStopped != nil {
		if err := checkStopped(ctx, pidFile); err != nil {
			return err
		}
	}

	var errs []error
	ranHelper := false
	if snap != nil && snap.CNIBinDir() != "" {
		cniBinDir := snap.CNIBinDir()
		helper := filepath.Join(cniBinDir, "cilium-dbg")
		info, helperErr := statHelper(helper)
		if errors.Is(helperErr, os.ErrNotExist) {
			if snap.Strict() {
				if _, cniErr := os.Stat(filepath.Join(cniBinDir, "cilium-cni")); cniErr == nil {
					errs = append(errs, fmt.Errorf("Cilium cleanup helper is missing: %s", helper))
				} else if !errors.Is(cniErr, os.ErrNotExist) {
					errs = append(errs, cniErr)
				}
			}
		} else if helperErr != nil {
			errs = append(errs, fmt.Errorf("read Cilium cleanup helper: %w", helperErr))
		} else if err := validateCleanupHelper(info); err != nil {
			errs = append(errs, err)
		} else {
			ranHelper = true
			if err := runCleanup(ctx, helper, pidFile); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if !ranHelper && pidFile != "" {
		if err := os.Remove(pidFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove stopped Cilium PID file: %w", err))
		}
	}

	if ctx.Err() != nil {
		return errors.Join(append(errs, ctx.Err())...)
	}
	if removeDirectories != nil {
		if err := removeDirectories(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func ensureCiliumAgentStopped(ctx context.Context, pidFile, procDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if pidFile != "" {
		data, err := os.ReadFile(pidFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read Cilium PID file %s: %w", pidFile, err)
		}
		if err == nil {
			if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
				pid, parseErr := strconv.Atoi(trimmed)
				if parseErr != nil || pid <= 1 {
					return fmt.Errorf("invalid Cilium PID in %s: %q", pidFile, trimmed)
				}
				running, inspectErr := isLiveCiliumAgentPID(procDir, strconv.Itoa(pid))
				if inspectErr != nil {
					return inspectErr
				}
				if running {
					return fmt.Errorf("refusing Cilium cleanup while PID %d is active: %w", pid, errCiliumAgentRunning)
				}
			}
		}
	}
	if procDir == "" {
		return nil
	}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return fmt.Errorf("read %s to verify Cilium agent stopped: %w", procDir, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		pid, parseErr := strconv.Atoi(name)
		if parseErr != nil || pid <= 1 {
			continue
		}
		running, inspectErr := isLiveCiliumAgentPID(procDir, name)
		if inspectErr != nil {
			return inspectErr
		}
		if running {
			return fmt.Errorf("refusing Cilium cleanup while PID %s is active: %w", name, errCiliumAgentRunning)
		}
	}
	return nil
}

func isLiveCiliumAgentPID(procDir, pid string) (bool, error) {
	comm, err := os.ReadFile(filepath.Join(procDir, pid, "comm"))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect process %s comm: %w", pid, err)
	}
	commName := strings.TrimSpace(string(comm))
	switch commName {
	case "cilium-agent":
	case "kubelet", "containerd":
		if os.Getenv("SNAP_CONTEXT") == "" {
			return false, nil
		}
		exe, exeErr := os.Readlink(filepath.Join(procDir, pid, "exe"))
		if errors.Is(exeErr, os.ErrNotExist) || errors.Is(exeErr, syscall.ESRCH) {
			return false, nil
		}
		if exeErr != nil {
			return false, fmt.Errorf("inspect process %s exe: %w", pid, exeErr)
		}
		if !strings.Contains(exe, "/snap/k8s") {
			return false, nil
		}
	default:
		return false, nil
	}
	stat, err := os.ReadFile(filepath.Join(procDir, pid, "stat"))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect cilium-agent PID %s stat: %w", pid, err)
	}
	end := strings.LastIndex(string(stat), ") ")
	if end < 0 || len(stat) <= end+2 {
		return true, nil
	}
	fields := strings.Fields(string(stat[end+2:]))
	if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
		return false, nil
	}
	return true, nil
}

func validateCleanupHelper(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	euid := uint32(os.Geteuid())
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o100 == 0 || !ok || (stat.Uid != 0 && stat.Uid != euid) {
		return fmt.Errorf("Cilium cleanup helper must be a root-owned executable, not writable by group or others")
	}
	return nil
}

func runCiliumCleanup(ctx context.Context, helper, pidFile string) error {
	if err := os.Remove(pidFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stopped Cilium PID file: %w", err)
	}
	cmd := exec.CommandContext(ctx, helper, "post-uninstall-cleanup", "--all-state", "--force")
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cilium-dbg cleanup failed: %w", err)
	}
	return nil
}
