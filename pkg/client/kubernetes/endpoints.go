package kubernetes

import (
	"context"
	"fmt"

	"github.com/canonical/k8sd/pkg/utils"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// GetKubeAPIServerEndpoints retrieves the known kube-apiserver endpoints of the cluster.
// GetKubeAPIServerEndpoints returns an error if the list of endpoints is empty.
func (c *Client) GetKubeAPIServerEndpoints(ctx context.Context) ([]string, error) {
	var endpointSlices *discoveryv1.EndpointSliceList
	var err error
	err = retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
		endpointSlices, err = c.DiscoveryV1().EndpointSlices("default").List(ctx, metav1.ListOptions{
			LabelSelector: "kubernetes.io/service-name=kubernetes",
		})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints for kubernetes service: %w", err)
	}
	if endpointSlices == nil {
		return nil, fmt.Errorf("endpoints for kubernetes service not found")
	}

	addresses := utils.ParseEndpointSlices(endpointSlices)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("empty list of endpoints for the kubernetes service")
	}

	return addresses, nil
}

// HasReadyEndpoints checks whether the specified service in the namespace has at least one ready endpoint.
// It checks EndpointSlices first (matching label kubernetes.io/service-name=<serviceName>),
// falling back to Endpoints.
func (c *Client) HasReadyEndpoints(ctx context.Context, namespace, serviceName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	endpointSlices, err := c.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", discoveryv1.LabelServiceName, serviceName),
	})
	if err == nil && endpointSlices != nil && len(endpointSlices.Items) > 0 {
		for _, slice := range endpointSlices.Items {
			for _, ep := range slice.Endpoints {
				ready := ep.Conditions.Ready == nil || *ep.Conditions.Ready
				if ready && len(ep.Addresses) > 0 {
					return true, nil
				}
			}
		}
	}

	endpoints, err := c.CoreV1().Endpoints(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err == nil && endpoints != nil {
		for _, subset := range endpoints.Subsets {
			if len(subset.Addresses) > 0 {
				return true, nil
			}
		}
	}

	return false, nil
}
