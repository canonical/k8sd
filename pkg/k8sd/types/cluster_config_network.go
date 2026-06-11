package types

type Network struct {
	Enabled          *bool   `json:"enabled,omitempty"`
	PodCIDR          *string `json:"pod-cidr,omitempty"`
	ServiceCIDR      *string `json:"service-cidr,omitempty"`
	KubeProxyEnabled *bool   `json:"kube-proxy-enabled,omitempty"`
}

func (c Network) GetEnabled() bool       { return getField(c.Enabled) }
func (c Network) GetPodCIDR() string     { return getField(c.PodCIDR) }
func (c Network) GetServiceCIDR() string { return getField(c.ServiceCIDR) }
func (c Network) GetKubeProxyEnabled() bool {
	if c.KubeProxyEnabled == nil {
		// Default: when the network feature is enabled, kube-proxy replacement is implied.
		return !c.GetEnabled()
	}
	return getField(c.KubeProxyEnabled)
}
func (c Network) Empty() bool { return c == Network{} }
