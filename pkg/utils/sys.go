package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func hasPathPrefix(p, prefix string) bool {
	if prefix == "" {
		return false
	}
	cleanPrefix := filepath.Clean(prefix)
	if cleanPrefix == "." || cleanPrefix == string(filepath.Separator) {
		return false
	}
	cleanPath := filepath.Clean(p)
	return cleanPath == cleanPrefix || strings.HasPrefix(cleanPath, cleanPrefix+string(filepath.Separator))
}

func isSnapPath(p, snapDir string) bool {
	if snapDir == "" || p == "" {
		return false
	}
	cleanSnap := filepath.Clean(snapDir)
	if cleanSnap == "." || cleanSnap == string(filepath.Separator) {
		return false
	}
	snapCurrent := filepath.Join(filepath.Dir(cleanSnap), "current")
	return hasPathPrefix(p, cleanSnap) || hasPathPrefix(p, snapCurrent)
}

func stripSnapPaths(pathList, snapDir, snapLibPath string) string {
	if snapDir == "" || pathList == "" {
		return pathList
	}
	snapLibDirs := make(map[string]struct{})
	for _, d := range filepath.SplitList(snapLibPath) {
		if d != "" {
			snapLibDirs[filepath.Clean(d)] = struct{}{}
		}
	}
	var kept []string
	for _, entry := range filepath.SplitList(pathList) {
		if entry == "" {
			continue
		}
		if isSnapPath(entry, snapDir) {
			continue
		}
		if _, ok := snapLibDirs[filepath.Clean(entry)]; ok {
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// CleanHostEnviron filters an environment slice so host binaries do not inherit
// snap-specific library or binary search paths that conflict with host glibc.
func CleanHostEnviron(environ []string) []string {
	var (
		snapDir     string
		snapLibPath string
		realLDPath  string
		hasRealLD   bool
		realPath    string
		hasRealPath bool
		existingLD  string
		hasLD       bool
		existingP   string
		hasP        bool
	)

	for _, env := range environ {
		key, val, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		switch key {
		case "SNAP":
			snapDir = val
		case "SNAP_LIBRARY_PATH":
			snapLibPath = val
		case "REAL_LD_LIBRARY_PATH":
			realLDPath = val
			hasRealLD = true
		case "REAL_PATH":
			realPath = val
			hasRealPath = true
		case "LD_LIBRARY_PATH":
			existingLD = val
			hasLD = true
		case "PATH":
			existingP = val
			hasP = true
		}
	}

	inSnap := snapDir != ""

	var targetLD string
	var emitLD bool
	switch {
	case hasRealLD:
		targetLD = stripSnapPaths(realLDPath, snapDir, snapLibPath)
		emitLD = targetLD != ""
	case !inSnap && hasLD:
		targetLD = existingLD
		emitLD = true
	}

	var targetPath string
	var emitPath bool
	switch {
	case hasRealPath:
		if cleaned := stripSnapPaths(realPath, snapDir, ""); cleaned != "" {
			targetPath = cleaned
			emitPath = true
		} else if hasP {
			targetPath = stripSnapPaths(existingP, snapDir, "")
			emitPath = targetPath != ""
		}
	case inSnap && hasP:
		targetPath = stripSnapPaths(existingP, snapDir, "")
		emitPath = targetPath != ""
	case hasP:
		targetPath = existingP
		emitPath = true
	}

	var seenLD, seenPath bool
	result := make([]string, 0, len(environ))
	for _, env := range environ {
		key, _, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		switch key {
		case "LD_LIBRARY_PATH":
			if !seenLD {
				seenLD = true
				if emitLD {
					result = append(result, "LD_LIBRARY_PATH="+targetLD)
				}
			}
		case "PATH":
			if !seenPath {
				seenPath = true
				if emitPath {
					result = append(result, "PATH="+targetPath)
				}
			}
		default:
			result = append(result, env)
		}
	}

	if !seenLD && emitLD {
		result = append(result, "LD_LIBRARY_PATH="+targetLD)
	}
	if !seenPath && emitPath {
		result = append(result, "PATH="+targetPath)
	}

	return result
}

// HostEnviron returns a copy of os.Environ() sanitized for running host binaries.
func HostEnviron() []string {
	return CleanHostEnviron(os.Environ())
}

func lookPathInEnviron(file string, environ []string) string {
	var pathEnv string
	for _, kv := range environ {
		if val, ok := strings.CutPrefix(kv, "PATH="); ok {
			pathEnv = val
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, file)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 && unix.Access(candidate, unix.X_OK) == nil {
			return candidate
		}
	}
	return ""
}

// HostCommandContext creates an *exec.Cmd configured to run a host binary with
// an environment and executable lookup sanitized of snap-specific search paths.
func HostCommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	env := HostEnviron()
	if !strings.ContainsRune(name, filepath.Separator) {
		if resolved := lookPathInEnviron(name, env); resolved != "" {
			cmd := exec.CommandContext(ctx, resolved, arg...)
			cmd.Args[0] = name
			cmd.Env = env
			return cmd
		}
		cmd := exec.CommandContext(ctx, name, arg...)
		if cmd.Err == nil && isSnapPath(cmd.Path, os.Getenv("SNAP")) {
			return cmd
		}
		cmd.Env = env
		return cmd
	}
	cmd := exec.CommandContext(ctx, name, arg...)
	cmd.Env = env
	return cmd
}

func runCmd(cmd *exec.Cmd, command []string, opts ...func(*exec.Cmd)) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	for _, o := range opts {
		o(cmd)
	}

	if err := cmd.Run(); err != nil {
		if cmd.ProcessState == nil {
			return fmt.Errorf("command %v failed: %w", command, err)
		}
		return fmt.Errorf("command %v failed with exit code %d: %w", command, cmd.ProcessState.ExitCode(), err)
	}
	return nil
}

// RunCommand executes a command with a given context.
// RunCommand returns nil if the command completes successfully and the exit code is 0.
func RunCommand(ctx context.Context, command []string, opts ...func(*exec.Cmd)) error {
	if len(command) == 0 {
		return fmt.Errorf("empty command")
	}
	return runCmd(exec.CommandContext(ctx, command[0], command[1:]...), command, opts...)
}

// RunHostCommand executes a host command with a sanitized environment.
func RunHostCommand(ctx context.Context, command []string, opts ...func(*exec.Cmd)) error {
	if len(command) == 0 {
		return fmt.Errorf("empty command")
	}
	return runCmd(HostCommandContext(ctx, command[0], command[1:]...), command, opts...)
}
