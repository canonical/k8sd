package kubernetes

import (
	"context"
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/log"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/client-go/util/retry"
)

func (c *Client) ListNodesStatuses(ctx context.Context) ([]apiv2.NodeStatus, error) {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})

	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	statuses := make([]apiv2.NodeStatus, len(nodes.Items))

	for i, node := range nodes.Items {
		statuses[i] = nodeStatusFromNode(&node)
	}

	return statuses, nil
}

// GetNodeStatus returns the status of a single node
func (c *Client) GetNodeStatus(ctx context.Context, nodeName string) (apiv2.NodeStatus, error) {
	node, err := c.GetNode(ctx, nodeName)
	if err != nil {
		return apiv2.NodeStatus{}, fmt.Errorf("failed to get node %q: %w", nodeName, err)
	}

	return nodeStatusFromNode(node), nil
}

// nodeStatusFromNode derives the cluster role, readiness, reachability and
// internal address from a Kubernetes node object.
func nodeStatusFromNode(node *v1.Node) apiv2.NodeStatus {
	nodeAddr := ""
	for _, addr := range node.Status.Addresses {
		if addr.Type == v1.NodeInternalIP {
			nodeAddr = addr.Address
		}
	}

	ready, reachable := false, false
	for _, cond := range node.Status.Conditions {
		if cond.Type == v1.NodeReady {
			ready = cond.Status == v1.ConditionTrue
			reachable = ready // TODO: This should be determined later.
		}
	}

	role := apiv2.ClusterRoleWorker
	if _, cp := node.Labels["node-role.kubernetes.io/control-plane"]; cp {
		role = apiv2.ClusterRoleControlPlane
	}

	return apiv2.NodeStatus{
		Name:        node.Name,
		Address:     nodeAddr,
		Reachable:   reachable,
		Ready:       ready,
		ClusterRole: role,
	}
}

func (c *Client) GetNode(ctx context.Context, nodeName string) (*v1.Node, error) {
	return c.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
}

// DeleteNode will remove a node from the kubernetes cluster.
// DeleteNode will retry if there is a conflict on the resource
// DeleteNode will retry if an internal server error occured (maximum of 5 times).
// DeleteNode will not fail if the node does not exist.
func (c *Client) DeleteNode(ctx context.Context, nodeName string) error {
	tries := 0
	retriable := func(err error) bool {
		if apierrors.IsConflict(err) {
			return true
		}

		tries++
		return apierrors.IsInternalError(err) && tries <= 5
	}

	return retry.OnError(retry.DefaultBackoff, retriable, func() error {
		if err := c.CoreV1().Nodes().Delete(ctx, nodeName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete node: %w", err)
		}
		return nil
	})
}

func (c *Client) WatchNode(ctx context.Context, name string, reconcile func(node *v1.Node) error) error {
	log := log.FromContext(ctx).WithValues("name", name)
	w, err := c.CoreV1().Nodes().Watch(ctx, metav1.SingleObject(metav1.ObjectMeta{Name: name}))
	if err != nil {
		return fmt.Errorf("failed to watch node name=%s: %w", name, err)
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-w.ResultChan():
			if !ok {
				return fmt.Errorf("watch closed")
			}
			node, ok := evt.Object.(*v1.Node)
			if !ok {
				return fmt.Errorf("expected a Node but received %#v", evt.Object)
			}

			if err := reconcile(node); err != nil {
				log.Error(err, "Reconcile Node failed")
			}
		}
	}
}

// NodeVersions returns a map of node names to their parsed Kubernetes versions.
func (c *Client) NodeVersions(ctx context.Context) (map[string]*versionutil.Version, error) {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodeVersions := make(map[string]*versionutil.Version)
	for _, node := range nodes.Items {
		v, err := versionutil.ParseGeneric(node.Status.NodeInfo.KubeletVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to parse version for node %s: %w", node.Name, err)
		}
		nodeVersions[node.Name] = v
	}

	return nodeVersions, nil
}
