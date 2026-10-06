package types

type Network struct {
	Enabled          *bool   `json:"enabled,omitempty"`
	PodCIDR          *string `json:"pod-cidr,omitempty"`
	ServiceCIDR      *string `json:"service-cidr,omitempty"`
	KubeProxyEnabled *bool   `json:"kube-proxy-enabled,omitempty"`
	// Patches is a list of Kustomize-style patches applied to the rendered
	// network feature manifests before they are installed/upgraded.
	Patches *[]Patch `json:"patches,omitempty"`
}

func (c Network) GetEnabled() bool       { return getField(c.Enabled) }
func (c Network) GetPodCIDR() string     { return getField(c.PodCIDR) }
func (c Network) GetServiceCIDR() string { return getField(c.ServiceCIDR) }
func (c Network) GetPatches() []Patch    { return getField(c.Patches) }
func (c Network) GetKubeProxyEnabled() bool {
	if c.KubeProxyEnabled == nil {
		// NOTE(HUE): we return true because in new clusters this field is explicitly set,
		// but on old clusters, we want to make sure kube-proxy is enabled unless explicitly set by admins.
		return true
	}
	return getField(c.KubeProxyEnabled)
}
func (c Network) Empty() bool { return c == Network{} }
