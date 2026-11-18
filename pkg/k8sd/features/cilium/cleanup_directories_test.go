package cilium

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/sys/mountinfo"
)

func makeCiliumState(t *testing.T, parent string) string {
	t.Helper()
	root := filepath.Join(parent, "cilium")
	if err := os.MkdirAll(filepath.Join(root, "state", "endpoint"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "endpoint", "config"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCleanupDirectoryDoesNotReadParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC directory read permission")
	}
	parent := t.TempDir()
	root := makeCiliumState(t, parent)
	if err := os.Chmod(parent, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if err := os.RemoveAll(root); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("RemoveAll should reproduce denied parent read: %v", err)
	}
	if err := removeCiliumDirectory(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned directory remains: %v", err)
	}
}

func TestCleanupDirectoryPreservesSymlinkTargets(t *testing.T) {
	parent := t.TempDir()
	root := makeCiliumState(t, parent)
	unrelated := filepath.Join(parent, "unrelated")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(unrelated, "keep")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unrelated, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	if err := removeCiliumDirectory(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated symlink target changed: %s %v", data, err)
	}
	if err := os.Symlink(unrelated, root); err != nil {
		t.Fatal(err)
	}
	if err := removeCiliumDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("symlink root accepted")
	}
	if _, err := os.Lstat(root); err != nil {
		t.Fatalf("refused symlink root changed: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink root target changed: %v", err)
	}
}

func TestCleanupDirectoryRefusesMountsBeforeDeletingAnything(t *testing.T) {
	for _, nested := range []string{"", "state/endpoint"} {
		t.Run(nested, func(t *testing.T) {
			root := makeCiliumState(t, t.TempDir())
			mounted := filepath.Join(root, nested)
			mounts := []*mountinfo.Info{{Mountpoint: mounted}}
			if err := removeCiliumDirectory(context.Background(), root, mounts); err == nil || !strings.Contains(err.Error(), "mount") {
				t.Fatalf("mount accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "state", "endpoint", "config")); err != nil {
				t.Fatalf("state changed despite mounted tree: %v", err)
			}
		})
	}
	root := makeCiliumState(t, t.TempDir())
	if err := removeCiliumDirectory(context.Background(), root, []*mountinfo.Info{{Mountpoint: root + "-unrelated"}}); err != nil {
		t.Fatalf("unrelated mount blocked cleanup: %v", err)
	}
}

func TestCleanupDirectoryHandlesAbsentEmptyAndInvalidRoots(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cilium")
	if err := removeCiliumDirectory(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeCiliumDirectory(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeCiliumDirectory(context.Background(), root, nil); err == nil {
		t.Fatal("non-directory root accepted")
	}
	if err := removeCiliumDirectory(context.Background(), filepath.Join(root, "child"), nil); err == nil {
		t.Fatal("invalid path accepted")
	}
	if data, err := os.ReadFile(root); err != nil || string(data) != "keep" {
		t.Fatalf("non-directory root changed: %s %v", data, err)
	}
}

func TestCleanupDirectoriesAggregatesErrorsAndContinues(t *testing.T) {
	parent := t.TempDir()
	refused := filepath.Join(parent, "refused")
	if err := os.WriteFile(refused, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := makeCiliumState(t, parent)
	otherRefused := filepath.Join(parent, "also-refused")
	if err := os.Symlink(refused, otherRefused); err != nil {
		t.Fatal(err)
	}
	err := removeCiliumDirectories(context.Background(), []string{refused, root, otherRefused}, func(mountinfo.FilterFunc) ([]*mountinfo.Info, error) { return nil, nil })
	if err == nil || !strings.Contains(err.Error(), refused) || !strings.Contains(err.Error(), otherRefused) {
		t.Fatalf("cleanup failures not aggregated: %v", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later owned directory not removed: %v", err)
	}
}

func TestCleanupDirectoryReportsUnreadableState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC directory read permission")
	}
	root := makeCiliumState(t, t.TempDir())
	nested := filepath.Join(root, "state")
	if err := os.Chmod(nested, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(nested, 0o700) })
	if err := removeCiliumDirectory(context.Background(), root, nil); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unreadable state failure hidden: %v", err)
	}
}

func TestCleanupDirectoriesFailsClosedWhenMountsCannotBeRead(t *testing.T) {
	root := makeCiliumState(t, t.TempDir())
	expected := errors.New("mountinfo unavailable")
	readMounts := func(mountinfo.FilterFunc) ([]*mountinfo.Info, error) { return nil, expected }
	if err := removeCiliumDirectories(context.Background(), []string{root}, readMounts); !errors.Is(err, expected) {
		t.Fatalf("mountinfo failure hidden: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "endpoint", "config")); err != nil {
		t.Fatalf("state deleted without checking mounts: %v", err)
	}
}

func TestCleanupDirectoryDetectsMountUnderSymlinkAncestor(t *testing.T) {
	parent := t.TempDir()
	root := makeCiliumState(t, parent)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	mounts := []*mountinfo.Info{{Mountpoint: filepath.Join(root, "state")}}
	if err := removeCiliumDirectory(context.Background(), filepath.Join(alias, "cilium"), mounts); err == nil {
		t.Fatal("mounted state hidden by symlink ancestor accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "state", "endpoint", "config")); err != nil {
		t.Fatalf("mounted state changed: %v", err)
	}
}

func TestCleanupDirectoryHonorsCancelledContext(t *testing.T) {
	root := makeCiliumState(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := removeCiliumDirectories(ctx, []string{root}, func(mountinfo.FilterFunc) ([]*mountinfo.Info, error) { return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "state", "endpoint", "config")); statErr != nil {
		t.Fatalf("cancelled cleanup deleted state: %v", statErr)
	}
}

func TestCleanupDirectoryRefusesSubtreeMountCreatedDuringTraversal(t *testing.T) {
	root := makeCiliumState(t, t.TempDir())
	calls := 0
	err := removeCiliumDirectoryWith(
		context.Background(),
		root,
		func(mountinfo.FilterFunc) ([]*mountinfo.Info, error) { return nil, nil },
		func(f *os.File) (mountIdentity, error) {
			id, err := statFileMountIdentity(f)
			if err != nil {
				return mountIdentity{}, err
			}
			calls++
			// Root check (1) and root dir open (2) match parent; first nested dir (3) simulates a newly mounted subtree.
			if calls >= 3 {
				id.mntID++
				id.hasMntID = true
			}
			return id, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "mounted") {
		t.Fatalf("expected concurrent subtree mount to be refused, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "state", "endpoint", "config")); statErr != nil {
		t.Fatalf("nested mounted state was deleted: %v", statErr)
	}
}
