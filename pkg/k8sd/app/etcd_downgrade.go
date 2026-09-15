package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/canonical/k8sd/pkg/etcd/downgrade"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/log"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// recoverEtcdDowngradeIfNeeded completes an interrupted etcd downgrade before
// the etcd service is started.
//
// After a snap downgrade across an etcd minor version boundary (e.g. k8s 1.37
// -> 1.36, etcd 3.7 -> 3.6) the bundled etcd binary refuses to start against a
// data directory stamped with the newer storage version. Normally the snap
// pre-refresh hook prepares the downgrade while the newer binary is still
// running, but that hook does not run for `snap revert` and may be missing on
// the source revision. In those cases we recover here: start a compatible
// (newer) etcd binary from a previous snap revision, run the etcd downgrade
// protocol, and only then let the bundled etcd start.
func (a *App) recoverEtcdDowngradeIfNeeded(ctx context.Context, s mctypes.State) error {
	log := log.FromContext(ctx).WithValues("step", "etcd-downgrade-recovery")

	isWorker, err := snaputil.IsWorker(a.snap)
	if err != nil {
		return fmt.Errorf("failed to check if node is a worker: %w", err)
	}
	if isWorker {
		return nil
	}

	cfg, err := databaseutil.GetClusterConfig(ctx, s)
	if err != nil {
		return fmt.Errorf("failed to get cluster config: %w", err)
	}
	if cfg.Datastore.GetType() != "etcd" {
		return nil
	}

	snapDir := filepath.Dir(a.snap.K8sBinDir())
	etcdDataDir := filepath.Join(a.snap.EtcdDir(), "data")

	needed, storageVersion, binaryVersion, err := downgrade.NeedsRecovery(snapDir, etcdDataDir)
	if err != nil {
		return fmt.Errorf("failed to check if etcd downgrade recovery is needed: %w", err)
	}
	if !needed {
		return nil
	}

	log.Info("etcd data directory requires downgrade recovery",
		"storage-version", storageVersion.String(), "binary-version", binaryVersion.String())

	localhostAddress, err := utils.GetLocalhostAddress()
	if err != nil {
		return fmt.Errorf("failed to get localhost address: %w", err)
	}
	endpoints := []string{fmt.Sprintf("https://%s", utils.JoinHostPort(localhostAddress.String(), cfg.Datastore.GetEtcdPort()))}

	return downgrade.Recover(ctx, log, downgrade.RecoverConfig{
		SnapDir:       snapDir,
		SnapCommonDir: a.snap.SnapCommonDir(),
		EtcdDataDir:   etcdDataDir,
		EtcdPKIDir:    a.snap.EtcdPKIDir(),
		EtcdEndpoints: endpoints,
		NewClient: func(endpoints []string) (downgrade.Client, error) {
			return a.snap.EtcdClient(endpoints)
		},
		StopEtcd: func(ctx context.Context) error {
			return a.snap.StopServices(ctx, []string{"etcd"})
		},
		StartEtcd: func(ctx context.Context) error {
			return a.snap.StartServices(ctx, []string{"etcd"})
		},
		Timeout: 5 * time.Minute,
	})
}
