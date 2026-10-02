package internal_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/snap/util/cleanup/internal"
	mountutils "github.com/canonical/k8sd/pkg/utils/mount"
)

func createFakeExe(t *testing.T, dir, name, script string) {
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(script), 0o755)
	if err != nil {
		t.Fatalf("failed to create fake exe %s: %v", name, err)
	}
}

func TestLogoutISCSISessions_NoIscsiadm(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	internal.LogoutISCSISessions(context.Background(), []string{"1"})
}

func TestLogoutISCSISessions_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	script := `#!/bin/sh
echo "iscsiadm $*" >> ` + filepath.Join(tmpDir, "calls.log") + `
exit 0
`
	createFakeExe(t, tmpDir, "iscsiadm", script)

	internal.LogoutISCSISessions(context.Background(), []string{"1", "2"})

	calls, _ := os.ReadFile(filepath.Join(tmpDir, "calls.log"))
	expected := "iscsiadm -m session -r 1 -u\niscsiadm -m session -r 2 -u\n"
	if string(calls) != expected {
		t.Errorf("expected calls %q, got %q", expected, string(calls))
	}
}

func TestSyncISCSIDevices_NoIscsiadm(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	internal.SyncISCSIDevices(context.Background())
}

func TestSyncISCSIDevices_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	iscsiadmScript := `#!/bin/sh
if [ "$1" = "-m" ] && [ "$2" = "session" ]; then
	echo "Target: iqn.2019-10.io.longhorn:test"
	echo "SID: 1"
	echo "Attached scsi disk sda          State: running"
	echo "Attached scsi disk sdb          State: running"
	exit 0
fi
`
	createFakeExe(t, tmpDir, "iscsiadm", iscsiadmScript)

	blockdevScript := `#!/bin/sh
echo "blockdev $*" >> ` + filepath.Join(tmpDir, "calls.log") + `
exit 0
`
	createFakeExe(t, tmpDir, "blockdev", blockdevScript)

	internal.SyncISCSIDevices(context.Background())

	calls, _ := os.ReadFile(filepath.Join(tmpDir, "calls.log"))
	expected := "blockdev --flushbufs /dev/sda\nblockdev --flushbufs /dev/sdb\n"
	if string(calls) != expected {
		t.Errorf("expected calls %q, got %q", expected, string(calls))
	}
}

func TestGetISCSISessionsToLogout(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	iscsiadmScript := `#!/bin/sh
if [ "$1" = "-m" ] && [ "$2" = "session" ]; then
	echo "Target: iqn.2019-10.io.longhorn:test1"
	echo "SID: 1"
	echo "Attached scsi disk sda          State: running"
	echo "Target: iqn.2019-10.io.longhorn:test2"
	echo "SID: 2"
	echo "Attached scsi disk sdb          State: running"
	echo "Target: iqn.2019-10.io.longhorn:test3"
	echo "SID: 3"
	echo "Attached scsi disk sdc          State: running"
	exit 0
fi
`
	createFakeExe(t, tmpDir, "iscsiadm", iscsiadmScript)

	lockFilesDir := filepath.Join(t.TempDir(), "lockfiles")
	os.MkdirAll(lockFilesDir, 0o755)

	s := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir:        lockFilesDir,
			ContainerdRootDir:   "/var/lib/containerd",
			ContainerdSocketDir: "/run/containerd",
		},
	}

	procMountsFile := filepath.Join(t.TempDir(), "mounts")
	mountsContent := `
/dev/sda /var/lib/kubelet/pods/123/volumes/a ext4 rw,relatime 0 0
/dev/sdb /var/lib/kubelet/plugins/kubernetes.io/csi/pv/test2/globalmount ext4 rw,relatime 0 0
/dev/sdc /mnt/unrelated ext4 rw,relatime 0 0
`
	os.WriteFile(procMountsFile, []byte(mountsContent), 0o644)
	mockHelper := mountutils.NewMockMountHelper(procMountsFile)

	sids := internal.GetISCSISessionsToLogout(context.Background(), s, mockHelper)

	if len(sids) != 2 {
		t.Fatalf("expected 2 sids, got %d: %v", len(sids), sids)
	}

	has1 := false
	has2 := false
	for _, sid := range sids {
		if sid == "1" {
			has1 = true
		}
		if sid == "2" {
			has2 = true
		}
	}
	if !has1 || !has2 {
		t.Errorf("expected SIDs 1 and 2, got %v", sids)
	}
}
