package controllers

import (
	"context"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/snap"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	"github.com/canonical/k8sd/pkg/snap/util/cleanup"
	"github.com/canonical/k8sd/pkg/utils/control"
	v1 "k8s.io/api/core/v1"
)

type NodeConfigurationController struct {
	snap      snap.Snap
	waitReady func()
	// reconciledCh is used to notify that the controller has finished its reconciliation loop.
	reconciledCh chan struct{}
}

func NewNodeConfigurationController(snap snap.Snap, waitReady func()) *NodeConfigurationController {
	return &NodeConfigurationController{
		snap:         snap,
		waitReady:    waitReady,
		reconciledCh: make(chan struct{}, 1),
	}
}

func (c *NodeConfigurationController) Run(ctx context.Context, getRSAKey func(context.Context) (*rsa.PublicKey, error)) {
	ctx = log.NewContext(ctx, log.FromContext(ctx).WithValues("controller", "node-configuration"))
	log := log.FromContext(ctx)

	log.Info("Waiting for node to be ready")
	// wait for microcluster node to be ready
	c.waitReady()

	log.Info("Starting node configuration controller")

	for {
		client, err := getNewK8sClientWithRetries(ctx, c.snap, false)
		if err != nil {
			log.Error(err, "Failed to create a Kubernetes client")
		}

		if err := client.WatchConfigMap(ctx, "kube-system", "k8sd-config", func(configMap *v1.ConfigMap) error {
			err := c.reconcile(ctx, configMap, getRSAKey)
			c.notifyReconciled()
			return err
		}); err != nil {
			// This also can fail during bootstrapping/start up when api-server is not ready
			// So the watch requests get connection refused replies
			log.WithValues("name", "k8sd-config", "namespace", "kube-system").Error(err, "Failed to watch configmap")
		}

		select {
		case c.reconciledCh <- struct{}{}:
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *NodeConfigurationController) reconcile(ctx context.Context, configMap *v1.ConfigMap, getRSAKey func(context.Context) (*rsa.PublicKey, error)) error {
	log := log.FromContext(ctx)

	key, err := getRSAKey(ctx)
	if err != nil {
		return fmt.Errorf("failed to load the RSA public key: %w", err)
	}
	nodeConfig, err := types.ConfigMapToClusterConfig(configMap.Data, key)
	if err != nil {
		return fmt.Errorf("failed to parse configmap data to cluster config: %w", err)
	}

	updateArgs := make(map[string]string)
	var deleteArgs []string

	for _, loop := range []struct {
		val *string
		arg string
	}{
		{arg: "--cloud-provider", val: nodeConfig.Kubelet.CloudProvider},
		{arg: "--cluster-dns", val: nodeConfig.Kubelet.ClusterDNS},
		{arg: "--cluster-domain", val: nodeConfig.Kubelet.ClusterDomain},
	} {
		switch {
		case loop.val == nil:
			// value is not set in the configmap, no-op
		case *loop.val == "":
			// value is set in the configmap to the empty string, delete argument
			deleteArgs = append(deleteArgs, loop.arg)
		case *loop.val != "":
			// value is set in the configmap, update argument
			updateArgs[loop.arg] = *loop.val
		}
	}

	mustRestartKubelet, err := snaputil.UpdateServiceArguments(c.snap, "kubelet", updateArgs, deleteArgs)
	if err != nil {
		return fmt.Errorf("failed to update kubelet arguments: %w", err)
	}

	if mustRestartKubelet {
		// This may fail if other controllers try to restart the services at the same time, hence the retry.
		if err := control.RetryFor(ctx, 5, 5*time.Second, func() error {
			if err := c.snap.RestartServices(ctx, []string{"kubelet"}); err != nil {
				return fmt.Errorf("failed to restart kubelet to apply node configuration: %w", err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed after retry: %w", err)
		}
	}

	// Handle kube-proxy based on Network config
	kubeProxyFree := nodeConfig.Network.GetKubeProxyFree()

	// Read current local state
	localState, err := snaputil.ReadLocalState(c.snap)
	if err != nil {
		return fmt.Errorf("failed to read local state: %w", err)
	}

	// Get current kube-proxy enabled state from local state
	currentKubeProxyEnabled := false
	if state, ok := localState.Services[snaputil.ServiceKubeProxy]; ok && state != nil {
		currentKubeProxyEnabled = state.Enabled
	}

	// Check if kube-proxy state needs to change
	desiredKubeProxyEnabled := !kubeProxyFree
	if currentKubeProxyEnabled != desiredKubeProxyEnabled {
		if kubeProxyFree {
			log.Info("Kube-proxy free mode enabled, stopping kube-proxy and updating local state")
			// First clean up iptables rules created by kube-proxy
			cleanup.RemoveKubeProxyRules(ctx, c.snap)
			// Then stop the kube-proxy service
			if err := control.RetryFor(ctx, 5, 5*time.Second, func() error {
				if err := c.snap.StopServices(ctx, []string{"kube-proxy"}); err != nil {
					return fmt.Errorf("failed to stop kube-proxy service: %w", err)
				}
				return nil
			}); err != nil {
				return fmt.Errorf("failed to stop kube-proxy after retry: %w", err)
			}
		} else {
			log.Info("Kube-proxy free mode disabled, starting kube-proxy and updating local state")
			if err := control.RetryFor(ctx, 5, 5*time.Second, func() error {
				if err := c.snap.StartServices(ctx, []string{"kube-proxy"}); err != nil {
					return fmt.Errorf("failed to start kube-proxy service: %w", err)
				}
				return nil
			}); err != nil {
				return fmt.Errorf("failed to start kube-proxy after retry: %w", err)
			}
		}

		// Update local state to persist the change
		localState.SetServiceEnabled(snaputil.ServiceKubeProxy, desiredKubeProxyEnabled)
		if err := snaputil.WriteLocalState(c.snap, localState); err != nil {
			return fmt.Errorf("failed to update local state: %w", err)
		}
	}

	return nil
}

// ReconciledCh returns the channel where the controller pushes when a reconciliation loop is finished.
func (c *NodeConfigurationController) ReconciledCh() <-chan struct{} {
	return c.reconciledCh
}

func (c *NodeConfigurationController) notifyReconciled() {
	select {
	case c.reconciledCh <- struct{}{}:
	default:
	}
}
