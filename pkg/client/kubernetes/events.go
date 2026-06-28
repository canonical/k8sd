package kubernetes

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ListEvents returns events in the namespace matching the list options.
// Caller is expected to filter / sort by EventTime or LastTimestamp.
func (c *Client) ListEvents(ctx context.Context, namespace string, listOptions metav1.ListOptions) ([]corev1.Event, error) {
	events, err := c.CoreV1().Events(namespace).List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	return events.Items, nil
}
