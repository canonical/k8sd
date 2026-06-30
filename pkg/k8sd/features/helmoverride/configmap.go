// Package helmoverride provides shared utilities for reading and merging
// Helm value overrides from Kubernetes ConfigMaps.
package helmoverride

import (
	"context"
	"fmt"

	"github.com/canonical/k8sd/pkg/snap"
	"gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetConfigMapOverrides reads the named ConfigMap from the kube-system namespace
// and parses its "values" key as YAML Helm values.
// Returns nil, nil if the ConfigMap does not exist, the "values" key is absent,
// or no Kubernetes client is available.
func GetConfigMapOverrides(ctx context.Context, snap snap.Snap, configMapName string) (map[string]any, error) {
	client, err := snap.KubernetesClient("")
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	if client == nil {
		// No client available — skip overrides silently.
		return nil, nil
	}

	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: failed to get configmap: %w", configMapName, err)
	}

	valuesYAML, ok := cm.Data["values"]
	if !ok {
		return nil, nil
	}

	overrides := make(map[string]any)
	if err := yaml.Unmarshal([]byte(valuesYAML), &overrides); err != nil {
		return nil, fmt.Errorf("%s: failed to parse values: %w", configMapName, err)
	}

	return overrides, nil
}

// MergeValues performs a deep merge of base and overlay maps.
// Values from overlay take precedence over base.
// Nested maps are recursively merged; all other types are replaced.
func MergeValues(base, overlay map[string]any) map[string]any {
	result := make(map[string]any)

	for k, v := range base {
		result[k] = v
	}

	for k, v := range overlay {
		if baseVal, exists := result[k]; exists {
			if baseMap, ok := baseVal.(map[string]any); ok {
				if overlayMap, ok := v.(map[string]any); ok {
					result[k] = MergeValues(baseMap, overlayMap)
					continue
				}
			}
		}
		result[k] = v
	}

	return result
}
