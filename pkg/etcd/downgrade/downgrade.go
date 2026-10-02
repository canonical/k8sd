package downgrade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils/control"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Client is the subset of the etcd client used by the downgrade orchestration.
// It is implemented by pkg/client/etcd.Client.
type Client interface {
	// Downgrade issues a downgrade request (validate/enable/cancel) for the
	// given target version, e.g. "3.6".
	Downgrade(ctx context.Context, action clientv3.DowngradeAction, version string) (*clientv3.DowngradeResponse, error)
	// Status returns the status of the member at the given endpoint.
	Status(ctx context.Context, endpoint string) (*clientv3.StatusResponse, error)
	// Endpoints returns the list of endpoints the client is connected to.
	Endpoints() []string
	// Close closes the client.
	Close() error
}

// PrepareDowngrade validates and enables an etcd cluster downgrade to the
// given target version, then waits until the local member's storage version
// has been migrated to the target.
//
// The operation is idempotent: if a downgrade to the target is already in
// progress (e.g. enabled by another cluster node refreshing at the same time),
// validation is skipped and the in-progress downgrade is awaited.
//
// It must be called while the cluster is still running the newer etcd binary,
// i.e. before the local etcd binary is replaced by the older one.
func PrepareDowngrade(ctx context.Context, lg log.Logger, client Client, target Version, timeout time.Duration) error {
	lg.Info("Preparing etcd downgrade", "target-version", target.String())

	if err := validate(ctx, lg, client, target); err != nil {
		return err
	}

	if err := enable(ctx, lg, client, target); err != nil {
		return err
	}

	if err := WaitForStorageVersion(ctx, lg, client, target, timeout); err != nil {
		return err
	}

	lg.Info("etcd downgrade prepared successfully", "target-version", target.String())
	return nil
}

// validate checks that the cluster can be downgraded to the target version.
// A downgrade that is already in progress is treated as success to keep
// concurrent invocations on multiple nodes idempotent.
func validate(ctx context.Context, lg log.Logger, client Client, target Version) error {
	_, err := client.Downgrade(ctx, clientv3.DowngradeValidate, target.String())
	switch {
	case err == nil:
		lg.Info("etcd downgrade validated", "target-version", target.String())
		return nil
	case errors.Is(err, rpctypes.ErrDowngradeInProcess):
		lg.Info("etcd downgrade already in progress, skipping validation", "target-version", target.String())
		return nil
	default:
		return fmt.Errorf("failed to validate etcd downgrade to %q: %w", target.String(), err)
	}
}

// enable starts the cluster downgrade to the target version. Enabling an
// already in-progress downgrade is treated as success.
func enable(ctx context.Context, lg log.Logger, client Client, target Version) error {
	_, err := client.Downgrade(ctx, clientv3.DowngradeEnable, target.String())
	switch {
	case err == nil:
		lg.Info("etcd downgrade enabled", "target-version", target.String())
		return nil
	case errors.Is(err, rpctypes.ErrDowngradeInProcess):
		lg.Info("etcd downgrade already in progress, skipping enable", "target-version", target.String())
		return nil
	default:
		return fmt.Errorf("failed to enable etcd downgrade to %q: %w", target.String(), err)
	}
}

// WaitForStorageVersion polls the local member until its storage version has
// been migrated to the target version. etcd migrates the storage schema of
// each member automatically once the downgrade is enabled.
func WaitForStorageVersion(ctx context.Context, lg log.Logger, client Client, target Version, timeout time.Duration) error {
	endpoints := client.Endpoints()
	if len(endpoints) == 0 {
		return fmt.Errorf("etcd client has no endpoints")
	}
	endpoint := endpoints[0]

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return control.WaitUntilReady(ctx, func() (bool, error) {
		status, err := client.Status(ctx, endpoint)
		if err != nil {
			// The member may be briefly unavailable while migrating; keep polling.
			return false, nil
		}
		storageVersion, err := ParseVersion(status.StorageVersion)
		if err != nil {
			return false, fmt.Errorf("failed to parse storage version %q: %w", status.StorageVersion, err)
		}
		if !storageVersion.Equal(target) {
			return false, nil
		}
		lg.Info("etcd storage version migrated to downgrade target", "storage-version", storageVersion.String())
		return true, nil
	})
}

// CancelDowngrade cancels an in-progress etcd downgrade. It is a no-op if no
// downgrade is in progress.
func CancelDowngrade(ctx context.Context, lg log.Logger, client Client) error {
	_, err := client.Downgrade(ctx, clientv3.DowngradeCancel, "")
	switch {
	case err == nil:
		lg.Info("Cancelled in-progress etcd downgrade")
		return nil
	case errors.Is(err, rpctypes.ErrNoInflightDowngrade):
		return nil
	default:
		return fmt.Errorf("failed to cancel etcd downgrade: %w", err)
	}
}
