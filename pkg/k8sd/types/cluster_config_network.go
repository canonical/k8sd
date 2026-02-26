package types

type Network struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	PodCIDR       *string `json:"pod-cidr,omitempty"`
	ServiceCIDR   *string `json:"service-cidr,omitempty"`
	KubeProxyFree *bool   `json:"kube-proxy-free,omitempty"`
}

func (c Network) GetEnabled() bool       { return getField(c.Enabled) }
func (c Network) GetPodCIDR() string     { return getField(c.PodCIDR) }
func (c Network) GetServiceCIDR() string { return getField(c.ServiceCIDR) }
func (c Network) GetKubeProxyFree() bool { return getField(c.KubeProxyFree) }
func (c Network) Empty() bool            { return c == Network{} }
