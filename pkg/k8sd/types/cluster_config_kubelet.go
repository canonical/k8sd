package types

type Kubelet struct {
	CloudProvider      *string   `json:"cloud-provider,omitempty"`
	ClusterDNS         *string   `json:"cluster-dns,omitempty"`
	ClusterDomain      *string   `json:"cluster-domain,omitempty"`
	ControlPlaneTaints *[]string `json:"control-plane-taints,omitempty"`
}

func (c Kubelet) GetCloudProvider() string        { return getField(c.CloudProvider) }
func (c Kubelet) GetClusterDNS() string           { return getField(c.ClusterDNS) }
func (c Kubelet) GetClusterDomain() string        { return getField(c.ClusterDomain) }
func (c Kubelet) GetControlPlaneTaints() []string { return getField(c.ControlPlaneTaints) }
func (c Kubelet) Empty() bool                     { return c == Kubelet{} }
