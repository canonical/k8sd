package preflight

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SnapDownloader downloads and extracts a target snap to inspect its components.
type SnapDownloader struct {
	runCommand func(ctx context.Context, command []string) (string, error)
}

// NewSnapDownloader creates a SnapDownloader using the provided command runner.
func NewSnapDownloader(runCommand func(ctx context.Context, command []string) (string, error)) *SnapDownloader {
	return &SnapDownloader{runCommand: runCommand}
}

// SnapDownloaderFromSnap creates a SnapDownloader that uses a snap runner.
// The snapRunner should run shell commands and return their combined stdout/stderr.
func SnapDownloaderFromSnap() *SnapDownloader {
	return &SnapDownloader{
		runCommand: runCommandWithOutput,
	}
}

// DownloadTargetSnap downloads the snap for a target channel and extracts images.txt.
// Returns the list of ComponentInfo from the target snap and its version string.
func (d *SnapDownloader) DownloadTargetSnap(ctx context.Context, channel string) ([]ComponentInfo, string, error) {
	tmpDir, err := os.MkdirTemp("", "preflight-")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	snapFile := filepath.Join(tmpDir, "target.snap")
	assertFile := filepath.Join(tmpDir, "target.assert")

	// 1. snap download k8s --channel=<channel>
	downloadCmd := []string{"snap", "download", "k8s", "--channel", channel, "--target-directory", tmpDir}
	if _, err := d.runCommand(ctx, downloadCmd); err != nil {
		return nil, "", fmt.Errorf("failed to download target snap: %w", err)
	}

	// snap download renames files; find the actual .snap file
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read download directory: %w", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".snap") {
			snapFile = filepath.Join(tmpDir, entry.Name())
		} else if strings.HasSuffix(entry.Name(), ".assert") {
			assertFile = filepath.Join(tmpDir, entry.Name())
		}
	}

	// 2. unsquashfs -d <tmpdir>/fs <snapfile>
	unsquashDir := filepath.Join(tmpDir, "fs")
	unsquashCmd := []string{"unsquashfs", "-d", unsquashDir, snapFile}
	if _, err := d.runCommand(ctx, unsquashCmd); err != nil {
		return nil, "", fmt.Errorf("failed to extract target snap: %w", err)
	}

	// 3. Read images.txt from extracted snap
	imagesPath := filepath.Join(unsquashDir, "images.txt")
	imagesData, err := os.ReadFile(imagesPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read images.txt from target snap: %w", err)
	}

	imagesList := strings.Split(strings.TrimSpace(string(imagesData)), "\n")
	components := ParseImageLines(imagesList)

	// 4. Read snap version from meta/snap.yaml
	version := ""
	metaPath := filepath.Join(unsquashDir, "meta", "snap.yaml")
	if metaData, err := os.ReadFile(metaPath); err == nil {
		for _, line := range strings.Split(string(metaData), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "version:") {
				version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "version:"))
				break
			}
		}
	}

	// Clean up downloaded files
	os.Remove(snapFile)
	os.Remove(assertFile)

	return components, version, nil
}

// runCommandWithOutput runs a command and returns its combined stdout+stderr.
func runCommandWithOutput(ctx context.Context, command []string) (string, error) {
	var args []string
	if len(command) > 1 {
		args = command[1:]
	}
	cmd := exec.CommandContext(ctx, command[0], args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("command %v failed: %w\n%s", command, err, buf.String())
	}
	return strings.TrimSpace(buf.String()), nil
}
