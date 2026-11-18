package kubernetes

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetServiceClusterIP retrieves the ClusterIP from a Kubernetes service.
func (c *Client) GetServiceClusterIP(ctx context.Context, name, namespace string) (string, error) {
	svc, err := c.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service '%s' in namespace '%s': %w", name, namespace, err)
	}

	return svc.Spec.ClusterIP, nil
}

// FindServiceByClusterIP returns the Service in any namespace that holds ip as one of its ClusterIPs.
// FindServiceByClusterIP returns false if no Service holds ip.
func (c *Client) FindServiceByClusterIP(ctx context.Context, ip string) (corev1.Service, bool, error) {
	services, err := c.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return corev1.Service{}, false, fmt.Errorf("failed to list services: %w", err)
	}
	for i := range services.Items {
		if svc := &services.Items[i]; svc.Spec.ClusterIP == ip || slices.Contains(svc.Spec.ClusterIPs, ip) {
			return *svc, true, nil
		}
	}
	return corev1.Service{}, false, nil
}
