package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations"
	databaseutil "github.com/canonical/k8sd/pkg/k8sd/database/util"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/canonical/k8sd/pkg/utils/control"
	"github.com/canonical/k8sd/pkg/utils/node"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

// postClusterRemove handles requests to remove a node from the cluster.
// It will remove the node from etcd, microcluster and from Kubernetes.
// If force is true, the node is removed on a best-effort basis even if it is not reachable.
func (e *Endpoints) postClusterRemove(s mctypes.State, r *http.Request) mctypes.Response {
	snap := e.provider.Snap()

	req := apiv2.RemoveNodeRequest{}
	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if req.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	log := log.FromContext(ctx).WithValues("name", req.Name, "force", req.Force)

	cfg, err := databaseutil.GetClusterConfig(ctx, s)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get cluster config: %w", err))
	}

	isControlPlane, err := node.IsControlPlaneNode(ctx, s, req.Name, e.provider.Snap())
	if err != nil {
		if req.Force {
			log.Error(err, "Failed to determine if node is control-plane, but continuing due to force=true")
		} else {
			return mctypes.InternalError(fmt.Errorf("failed to determine if node is control-plane: %w", err))
		}
	}

	if _, ok := cfg.Annotations[apiv1_annotations.AnnotationSkipCleanupKubernetesNodeOnRemove]; !ok {
		log.Info("Remove node from Kubernetes cluster")
		if err := removeNodeFromKubernetes(ctx, snap, req.Name); err != nil {
			if req.Force {
				// With force=true, we want to cleanup all out-of-sync mentions of this node.
				// It might be that the node is already gone from k8s, but not from microcluster.
				// So we log the error, but continue.
				log.Error(err, "Failed to remove node from Kubernetes, but continuing due to force=true")
			} else {
				return mctypes.InternalError(fmt.Errorf("failed to remove node from Kubernetes: %w", err))
			}
		}
	} else {
		log.Info("Skipping Kubernetes node removal as per annotation")
	}

	// The control-plane check relies on the microcluster membership being correct. If the membership is out-of-sync, we might
	// mis-classify a control-plane node as a worker node. Hence we always proceed with the removal
	// if force=true, regardless of the role of the node.
	if isControlPlane || req.Force {
		log.Info("Remove node from datastore")
		if err := removeNodeFromDatastore(ctx, s, snap, req.Name, cfg); err != nil {
			if req.Force {
				// With force=true, we want to cleanup all out-of-sync mentions of this node.
				// So we log the error, but continue.
				log.Error(err, "Failed to remove node from datastore, but continuing due to force=true; ignore error for workers", "datastore", cfg.Datastore.GetType())
			} else {
				return mctypes.InternalError(fmt.Errorf("failed to delete node from datastore: %w", err))
			}
		}

		log.Info("Remove node from microcluster")
		if err := removeNodeFromMicrocluster(ctx, s, req.Name, req.Force, snap); err != nil {
			if req.Force {
				log.Error(err, "Failed to remove node from microcluster, but continuing due to force=true; ignore error for workers")
			} else {
				return mctypes.InternalError(fmt.Errorf("failed to delete node from microcluster: %w", err))
			}
		}
	}

	return mctypes.SyncResponse(true, &apiv2.RemoveNodeResponse{})
}

func removeNodeFromDatastore(ctx context.Context, s mctypes.State, snap snap.Snap, nodeName string, clusterConfig types.ClusterConfig) error {
	switch clusterConfig.Datastore.GetType() {
	case "etcd":
		if err := removeNodeFromEtcd(ctx, snap, s, clusterConfig, nodeName); err != nil {
			return fmt.Errorf("failed to remove node from etcd cluster: %w", err)
		}
	case "external":
		// The admin is responsible for cleaning up the external datastore membership.
	default:
	}

	return nil
}

func removeNodeFromEtcd(ctx context.Context, snap snap.Snap, s mctypes.State, cfg types.ClusterConfig, nodeName string) error {
	c, err := snap.K8sdClient("")
	if err != nil {
		return fmt.Errorf("failed to get k8sd client: %w", err)
	}

	members, err := c.GetClusterMembers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get microcluster members: %w", err)
	}

	clientURLs := make([]string, 0, len(members)-1)
	for _, member := range members {
		if member.Name == nodeName {
			// skip the node we want to remove
			continue
		}
		clientURLs = append(clientURLs, fmt.Sprintf("https://%s", utils.JoinHostPort(member.Address.Addr().String(), cfg.Datastore.GetEtcdPort())))
	}

	client, err := snap.EtcdClient(clientURLs)
	if err != nil {
		return fmt.Errorf("failed to create etcd client: %w", err)
	}
	defer client.Close()

	log := log.FromContext(ctx).WithValues("remove", "etcd", "name", nodeName, "clientURLs", clientURLs)
	log.Info("Deleting node from etcd cluster")
	if err := client.RemoveNodeByName(ctx, nodeName); err != nil {
		return fmt.Errorf("failed to remove node %s from etcd cluster: %w", nodeName, err)
	}

	return nil
}

func removeNodeFromMicrocluster(ctx context.Context, s mctypes.State, nodeName string, force bool, snap snap.Snap) error {
	log := log.FromContext(ctx).WithValues("name", nodeName)

	c, err := snap.K8sdClient("")
	if err != nil {
		return fmt.Errorf("failed to get k8sd client: %w", err)
	}

	maxRetries := 10
	var retries int
	if err := control.WaitUntilReady(ctx, func() (bool, error) {
		var notPending bool
		log.Info("Waiting for node to finish microcluster join before removing")
		member, err := c.GetClusterMember(ctx, s.Name())
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to get cluster member %q", s.Name()))
			retries++
		} else {
			// NOTE(Hue): We can not check for `cluster.Pending` with the `Role` type since it's internal to Microcluster
			notPending = member.Role != "PENDING"
		}

		if retries >= maxRetries {
			log.Info("Reached maximum number of retries for cluster member role check", "max_retries", maxRetries)
			return true, nil
		}

		return notPending, nil
	}); err != nil {
		log.Error(err, "Failed to wait for node to finish microcluster join before removing. Continuing with the cleanup...")
	}

	var nodeAddr string
	toBeRemovedMember, err := c.GetClusterMember(ctx, nodeName)
	if err != nil {
		log.Error(err, "Failed to get the microcluster member that is getting removed. Continuing with the cleanup...")
	} else {
		// NOTE(Hue): It's okay if we pass an empty string to the `RemoveClusterMember` call below
		// since the nodeAddr is only being used for specific edge cases on the Microcluster side.
		// e.g. when the name is no longer resolvable by dqlite.
		nodeAddr = toBeRemovedMember.Address.String()
	}

	// NOTE(hue): node removal process in CAPI might fail, we figured that the context passed to
	// `DeleteClusterMember` is somehow getting canceled but couldn't figure out why or by which component.
	// The cancellation happens after the `RunPreRemoveHook` call and before the `DeleteCoreClusterMember` call
	// in `clusterMemberDelete` endpoint of microcluster. This is a workaround to avoid the cancellation.
	// keep in mind that this failure is flaky and might not happen in every run.
	deleteCtx, deleteCancel := context.WithTimeout(mctypes.ContextWithLogger(context.Background()), 2*time.Minute)
	defer deleteCancel()
	log.Info("Deleting node from Microcluster cluster, for real")
	if err := c.RemoveClusterMember(deleteCtx, nodeName, nodeAddr, force); err != nil {
		return fmt.Errorf("failed to delete cluster member %s: %w", nodeName, err)
	}

	return nil
}

func removeNodeFromKubernetes(ctx context.Context, snap snap.Snap, nodeName string) error {
	log := log.FromContext(ctx)

	client, err := snap.KubernetesClient("")
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	log.Info("Deleting node from Kubernetes cluster")
	if err := client.DeleteNode(ctx, nodeName); err != nil {
		return fmt.Errorf("failed to remove k8s node %q: %w", nodeName, err)
	}

	return nil
}
