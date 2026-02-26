package types

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// configMapData represents the key-value data in a Kubernetes ConfigMap.
type configMapData map[string]string

// hash computes a deterministic SHA256 hash from the configmap data.
// json.Marshal ensures alphabetical key ordering for deterministic output.
func (d configMapData) hash() ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	h := sha256.New()
	if _, err := h.Write(data); err != nil {
		return nil, fmt.Errorf("failed to compute sha256: %w", err)
	}
	return h.Sum(nil), nil
}

// withoutSignature returns a copy of the data excluding the k8sd-mac field.
func (d configMapData) withoutSignature() configMapData {
	result := make(configMapData, len(d))
	for k, v := range d {
		if k != "k8sd-mac" {
			result[k] = v
		}
	}
	return result
}

// ClusterConfigToConfigMap converts ClusterConfig to a signed configmap.
// Only Kubelet fields and Network.KubeProxyFree are included.
func ClusterConfigToConfigMap(config ClusterConfig, key *rsa.PrivateKey) (map[string]string, error) {
	data := make(configMapData)

	// Kubelet fields
	if v := config.Kubelet.CloudProvider; v != nil {
		data["cloud-provider"] = *v
	}
	if v := config.Kubelet.ClusterDNS; v != nil {
		data["cluster-dns"] = *v
	}
	if v := config.Kubelet.ClusterDomain; v != nil {
		data["cluster-domain"] = *v
	}
	if v := config.Kubelet.ControlPlaneTaints; v != nil {
		taints, err := json.Marshal(*v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal control plane taints: %w", err)
		}
		data["control-plane-taints"] = string(taints)
	}

	// Network fields
	if v := config.Network.KubeProxyFree; v != nil {
		data["kube-proxy-free"] = fmt.Sprintf("%t", *v)
	}

	// Sign configmap data
	if key != nil {
		hash, err := data.hash()
		if err != nil {
			return nil, fmt.Errorf("failed to compute hash: %w", err)
		}
		mac, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash)
		if err != nil {
			return nil, fmt.Errorf("failed to sign hash: %w", err)
		}
		data["k8sd-mac"] = base64.StdEncoding.EncodeToString(mac)
	}

	return data, nil
}

// ConfigMapToClusterConfig parses and verifies a signed configmap.
// Returns ClusterConfig with Kubelet and Network.KubeProxyFree populated.
func ConfigMapToClusterConfig(m map[string]string, key *rsa.PublicKey) (ClusterConfig, error) {
	var config ClusterConfig

	if m == nil {
		return config, nil
	}

	// Verify signature first
	if key != nil {
		hash, err := configMapData(m).withoutSignature().hash()
		if err != nil {
			return ClusterConfig{}, fmt.Errorf("failed to compute config hash: %w", err)
		}
		signature, err := base64.StdEncoding.DecodeString(m["k8sd-mac"])
		if err != nil {
			return ClusterConfig{}, fmt.Errorf("failed to parse signature: %w", err)
		}
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash, signature); err != nil {
			return ClusterConfig{}, fmt.Errorf("failed to verify signature: %w", err)
		}
	}

	// Parse Kubelet fields
	if v, ok := m["cloud-provider"]; ok {
		config.Kubelet.CloudProvider = &v
	}
	if v, ok := m["cluster-dns"]; ok {
		config.Kubelet.ClusterDNS = &v
	}
	if v, ok := m["cluster-domain"]; ok {
		config.Kubelet.ClusterDomain = &v
	}
	if v, ok := m["control-plane-taints"]; ok {
		var taints []string
		if err := json.Unmarshal([]byte(v), &taints); err != nil {
			return ClusterConfig{}, fmt.Errorf("failed to parse control plane taints: %w", err)
		}
		config.Kubelet.ControlPlaneTaints = &taints
	}

	// Parse Network fields
	if v, ok := m["kube-proxy-free"]; ok {
		kubeProxyFree := v == "true"
		config.Network.KubeProxyFree = &kubeProxyFree
	}

	return config, nil
}
