package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations"
	k8sdclient "github.com/canonical/k8sd/pkg/client/k8sd"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/pki"
	"github.com/canonical/k8sd/pkg/k8sd/setup"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	"github.com/canonical/k8sd/pkg/snap/util/cleanup"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/canonical/k8sd/pkg/utils/control"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// NOTE(ben): the pre-remove performs a series of cleanup steps on a best-effort basis.
// If any step fails, the error is logged, and the cleanup continues, skipping dependent tasks.
// All steps need to be blocking as the context is cancelled after the hook returned.
func (a *App) onPreRemove(ctx context.Context, s mctypes.State, force bool) (rerr error) {
	snap := a.Snap()

	log := log.FromContext(ctx).WithValues("hook", "preremove", "node", s.Name())
	log.Info("Running preremove hook")

	c, err := snap.K8sdClient("")
	if err != nil {
		return fmt.Errorf("failed to get k8sd client: %w", err)
	}

	// NOTE (hue): in microcluster v2, PreRemove hook is also called if something goes wrong on
	// `bootstrap` and `join-cluster`. It is possible that we get stuck in this loop forever which causes
	// the `bootstrap` and `join-cluster` commands to hang and finally return an uninformative `context deadline exceeded` error
	// we optimistically stop trying after a fixed number of retries.
	if c == nil {
		// Can be nil if PreRemove fires during a bootstrap/join rollback, before a real client exists.
		log.Info("No k8sd client available, skipping PENDING wait")
	} else {
		maxRetries := 10
		var retries int
		if err := control.WaitUntilReady(ctx, func() (bool, error) {
			var notPending bool
			log.Info("Waiting for node to finish microcluster join before removing")
			member, err := c.GetClusterMember(ctx, s.Name())
			switch {
			case errors.Is(err, k8sdclient.ErrNotFound):
				// Node not found, no PENDING state to wait for.
				notPending = true
			case err != nil:
				log.Error(err, "Failed to get member")
				retries++
			default:
				notPending = member.Role != "PENDING"
			}

			if retries >= maxRetries {
				log.Info("Reached maximum number of retries for database transactions on pre-remove hook, continuing cleanup", "max_retries", maxRetries)
				return true, nil
			}

			return notPending, nil
		}); err != nil {
			log.Error(err, "Failed to wait for node to finish microcluster join before removing. Continuing with the cleanup...")
		}
	}

	cfg, err := databaseutil.GetClusterConfig(ctx, s)

	// PreRemove also runs when a join fails (microcluster's join rollback).
	// If the join reverter could not remove this node from the etcd cluster
	// after it became a voter, stopping etcd or deleting its data here would
	// lower the number of live voters and may lose quorum. Keep etcd running in
	// that case; it has to be removed manually from the etcd cluster first.
	keepEtcd := false
	if err == nil && cfg.Datastore.GetType() == "etcd" {
		if id, isVoter, voterErr := a.localEtcdVoter(ctx, s, c, cfg); voterErr != nil {
			log.Info("Could not determine etcd membership of this node, continuing with etcd cleanup", "error", voterErr.Error())
		} else if isVoter {
			keepEtcd = true
			log.Error(nil, "This node is still a voting member of the etcd cluster; leaving etcd running and its data in place to preserve quorum. "+
				"Remove the member manually once the etcd cluster is healthy, then stop etcd and delete the etcd data directory on this node",
				"memberID", fmt.Sprintf("%x", id), "remediation", fmt.Sprintf("etcdctl member remove %x", id))
		}
	}

	if err == nil {
		// NOTE(claudiub): We should only remove the certificates only if we're stopping the Kubernetes
		// services as well. Removing them without stopping the services will result in the services
		// being paralyzed and unable to continue their function, including potential Pod evictions
		// started by CAPI.
		if _, ok := cfg.Annotations.Get(apiv1_annotations.AnnotationSkipStopServicesOnRemove); !ok {
			// Perform all cleanup steps regardless of if this is a worker node or control plane.
			// Trying to detect the node type is not reliable as the node might have been marked as worker
			// or not, depending on which step it failed.
			log.Info("Cleaning up worker certificates")
			if _, err := setup.EnsureWorkerPKI(snap, &pki.WorkerNodePKI{}); err != nil {
				log.Error(err, "failed to cleanup worker certificates")
			}

			log.Info("Cleaning up control plane certificates")
			if _, err := setup.EnsureControlPlanePKI(snap, &pki.ControlPlanePKI{}); err != nil {
				log.Error(err, "failed to cleanup control plane certificates")
			}

			log.Info("Syncing iSCSI devices before stopping services")
			cleanup.SyncISCSIDevices(ctx)

			if keepEtcd {
				log.Info("Stopping all services except k8sd and etcd")
				if err := snaputil.StopK8sServicesExceptEtcd(ctx, snap, "--no-wait"); err != nil {
					log.Error(err, "failed to stop k8s services")
				}
			} else {
				log.Info("Stopping all services except k8sd")
				if err := snaputil.StopK8sServices(ctx, snap, "--no-wait"); err != nil {
					log.Error(err, "failed to stop k8s services")
				}
			}

			log.Info("Cleaning up containers")
			cleanup.TryCleanupContainers(ctx, snap)

			log.Info("Cleaning up containerd paths")
			cleanup.TryCleanupContainerdPaths(ctx, snap)
		} else {
			log.Info("Skipping service stop and certificate cleanup")
		}
	} else {
		log.Error(err, "Failed to retrieve cluster config")
	}

	if keepEtcd {
		log.Info("Keeping etcd directory, etcd certificates and etcd service arguments")
	} else {
		log.Info("Cleaning up external datastore certificates")
		if _, err := setup.EnsureExtDatastorePKI(snap, &pki.ExternalDatastorePKI{}); err != nil {
			log.Error(err, "Failed to cleanup external datastore certificates")
		}

		log.Info("Cleaning up etcd directory")
		if err := os.RemoveAll(snap.EtcdDir()); err != nil {
			log.Error(err, "failed to cleanup etcd state directory")
		}
	}

	for _, dir := range []string{snap.ServiceArgumentsDir()} {
		log.WithValues("directory", dir).Info("Cleaning up config files", dir)
		if keepEtcd {
			if err := removeAllExcept(dir, "etcd"); err != nil {
				log.WithValues("dir", dir).Error(err, "failed to delete config files", err)
			}
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.WithValues("dir", dir).Error(err, "failed to delete config files", err)
		}
	}

	log.Info("Removing worker node mark")
	if err := snaputil.MarkAsWorkerNode(snap, false); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Error(err, "failed to unmark node as worker")
		}
	}

	log.Info("Remove hook completed ")
	return nil
}

// etcdMembershipCheckTimeout bounds each etcd member list call of the
// pre-remove voter check.
const etcdMembershipCheckTimeout = 5 * time.Second

// etcdMemberLister lists the members of an etcd cluster.
type etcdMemberLister interface {
	MemberList(ctx context.Context, opts ...clientv3.OpOption) (*clientv3.MemberListResponse, error)
}

// localEtcdVoter reports whether this node is still a voting member of an
// etcd cluster that has other voters, and its etcd member ID.
func (a *App) localEtcdVoter(ctx context.Context, s mctypes.State, c k8sdclient.Client, cfg types.ClusterConfig) (uint64, bool, error) {
	// The hook context may already be cancelled during a join rollback.
	ctx = context.WithoutCancel(ctx)
	snap := a.Snap()

	var errs []error
	var cluster, local etcdMemberLister
	if c != nil {
		membersCtx, cancel := context.WithTimeout(ctx, etcdMembershipCheckTimeout)
		members, err := c.GetClusterMembers(membersCtx)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get microcluster members: %w", err))
		}
		endpoints := make([]string, 0, len(members))
		for _, member := range members {
			if member.Name == s.Name() {
				continue
			}
			endpoints = append(endpoints, fmt.Sprintf("https://%s", utils.JoinHostPort(member.Address.Addr().String(), cfg.Datastore.GetEtcdPort())))
		}
		if len(endpoints) > 0 {
			client, err := snap.EtcdClient(endpoints)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to create etcd client for cluster members: %w", err))
			} else {
				defer client.Close()
				cluster = client
			}
		}
	}

	client, err := snap.EtcdClient([]string{fmt.Sprintf("https://%s", utils.JoinHostPort(s.Address().Hostname(), cfg.Datastore.GetEtcdPort()))})
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to create etcd client for local member: %w", err))
	} else {
		defer client.Close()
		local = client
	}

	id, isVoter, err := etcdVoterWithPeers(ctx, s.Name(), cluster, local)
	if err != nil {
		return 0, false, errors.Join(append(errs, err)...)
	}
	return id, isVoter, nil
}

// etcdVoterWithPeers reports whether the etcd member called name is a voter
// in a cluster that has other voters, and its member ID. Stopping such a
// member lowers the number of live voters; stopping the only voter, a learner
// or a non-member does not affect anyone else.
//
// The linearizable view of the cluster (other members) is authoritative. The
// serializable view of the local member is only used when the cluster cannot
// be reached. Either lister may be nil.
func etcdVoterWithPeers(ctx context.Context, name string, cluster, local etcdMemberLister) (uint64, bool, error) {
	views := []struct {
		desc   string
		lister etcdMemberLister
		opts   []clientv3.OpOption
	}{
		{desc: "etcd cluster", lister: cluster},
		{desc: "local etcd member", lister: local, opts: []clientv3.OpOption{clientv3.WithSerializable()}},
	}

	var errs []error
	for _, view := range views {
		if view.lister == nil {
			continue
		}
		listCtx, cancel := context.WithTimeout(ctx, etcdMembershipCheckTimeout)
		resp, err := view.lister.MemberList(listCtx, view.opts...)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to list members of %s: %w", view.desc, err))
			continue
		}

		var id uint64
		var otherVoters int
		for _, m := range resp.Members {
			switch {
			case m.IsLearner:
			case m.Name == name:
				id = m.ID
			default:
				otherVoters++
			}
		}
		return id, id != 0 && otherVoters > 0, nil
	}
	if len(errs) == 0 {
		return 0, false, errors.New("no etcd endpoints available")
	}
	return 0, false, errors.Join(errs...)
}

// removeAllExcept removes every entry of dir except the one called keep.
func removeAllExcept(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
