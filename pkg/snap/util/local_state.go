package snaputil

import (
	"context"
	"fmt"
	"os"

	"github.com/canonical/k8sd/pkg/snap"
	"gopkg.in/yaml.v2"
)

const (
	// LocalNodeStateAPIVersion is the API version for the local node state file.
	LocalNodeStateAPIVersion = "k8sd.io/v1alpha1"
	// LocalNodeStateKind is the kind for the local node state file.
	LocalNodeStateKind = "LocalNodeState"
	// LocalNodeStateFileName is the name of the local state file.
	LocalNodeStateFileName = "local-state.yaml"
)

// Service is an enum for k8s service names.
type Service string

const (
	ServiceContainerd            Service = "containerd"
	ServiceEtcd                  Service = "etcd"
	ServiceKubeAPIServer         Service = "kube-apiserver"
	ServiceKubeControllerManager Service = "kube-controller-manager"
	ServiceKubeScheduler         Service = "kube-scheduler"
	ServiceKubelet               Service = "kubelet"
	ServiceKubeProxy             Service = "kube-proxy"
	ServiceK8sAPIServerProxy     Service = "k8s-apiserver-proxy"
)

// ServiceState represents per-service configuration.
type ServiceState struct {
	Enabled bool `yaml:"enabled"`
}

// LocalNodeState represents the local state file (Kubernetes-style format).
type LocalNodeState struct {
	APIVersion string                    `yaml:"apiVersion"`
	Kind       string                    `yaml:"kind"`
	Services   map[Service]*ServiceState `yaml:"services"`
}

// ReadLocalState reads the local state file.
func ReadLocalState(snap snap.Snap) (*LocalNodeState, error) {
	path := snap.LocalStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read local state file: %w", err)
	}

	state := &LocalNodeState{}
	if err := yaml.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("failed to parse local state file: %w", err)
	}

	return state, nil
}

// WriteLocalState writes the local state file.
func WriteLocalState(snap snap.Snap, state *LocalNodeState) error {
	path := snap.LocalStatePath()

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal local state: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write local state file: %w", err)
	}

	if err := os.Chown(path, snap.UID(), snap.GID()); err != nil {
		return fmt.Errorf("failed to chown local state file: %w", err)
	}

	return nil
}

// DeleteLocalState deletes the local state file.
func DeleteLocalState(snap snap.Snap) error {
	path := snap.LocalStatePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete local state file: %w", err)
	}
	return nil
}

// NewLocalState creates a new LocalNodeState from a services map.
func NewLocalState(services map[Service]*ServiceState) *LocalNodeState {
	return &LocalNodeState{
		APIVersion: LocalNodeStateAPIVersion,
		Kind:       LocalNodeStateKind,
		Services:   services,
	}
}

// EnabledServices returns the list of services that are enabled.
func (s *LocalNodeState) EnabledServices() []Service {
	var enabled []Service
	for service, state := range s.Services {
		if state != nil && state.Enabled {
			enabled = append(enabled, service)
		}
	}
	return enabled
}

// SetServiceEnabled sets the enabled state for a service.
func (s *LocalNodeState) SetServiceEnabled(service Service, enabled bool) {
	if s.Services == nil {
		s.Services = make(map[Service]*ServiceState)
	}
	s.Services[service] = &ServiceState{Enabled: enabled}
}

// StartEnabledServices starts all services that are enabled in the local state.
func StartEnabledServices(ctx context.Context, snap snap.Snap, state *LocalNodeState) error {
	services := state.EnabledServices()
	if len(services) == 0 {
		return nil
	}

	// Convert Service enum to strings for snap.StartServices
	serviceNames := make([]string, len(services))
	for i, service := range services {
		serviceNames[i] = string(service)
	}

	if err := snap.StartServices(ctx, serviceNames); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	return nil
}

// NewControlPlaneLocalState creates a local state for a control-plane node.
func NewControlPlaneLocalState(datastoreType string) *LocalNodeState {
	var etcdEnabled bool

	switch datastoreType {
	case "etcd":
		etcdEnabled = true
	case "external":
		etcdEnabled = false
	}

	services := map[Service]*ServiceState{
		ServiceContainerd:            {Enabled: true},
		ServiceEtcd:                  {Enabled: etcdEnabled},
		ServiceKubeAPIServer:         {Enabled: true},
		ServiceKubeControllerManager: {Enabled: true},
		ServiceKubeScheduler:         {Enabled: true},
		ServiceKubelet:               {Enabled: true},
		ServiceKubeProxy:             {Enabled: true},
		ServiceK8sAPIServerProxy:     {Enabled: false},
	}

	return NewLocalState(services)
}

// NewWorkerLocalState creates a local state for a worker node.
func NewWorkerLocalState() *LocalNodeState {
	services := map[Service]*ServiceState{
		ServiceContainerd:            {Enabled: true},
		ServiceEtcd:                  {Enabled: false},
		ServiceKubeAPIServer:         {Enabled: false},
		ServiceKubeControllerManager: {Enabled: false},
		ServiceKubeScheduler:         {Enabled: false},
		ServiceKubelet:               {Enabled: true},
		ServiceKubeProxy:             {Enabled: true},
		ServiceK8sAPIServerProxy:     {Enabled: true},
	}

	return NewLocalState(services)
}

// RestartEnabledServices restarts all services that are enabled in the local state.
func RestartEnabledServices(ctx context.Context, snap snap.Snap, state *LocalNodeState, extraSnapArgs ...string) error {
	services := state.EnabledServices()
	if len(services) == 0 {
		return nil
	}

	// Convert Service enum to strings for snap.RestartServices
	serviceNames := make([]string, len(services))
	for i, service := range services {
		serviceNames[i] = string(service)
	}

	if err := snap.RestartServices(ctx, serviceNames, extraSnapArgs...); err != nil {
		return fmt.Errorf("failed to restart services: %w", err)
	}

	return nil
}

// StopAllK8sServices stops all K8s services except k8sd (for cleanup/remove operations).
func StopAllK8sServices(ctx context.Context, snap snap.Snap, extraSnapArgs ...string) error {
	serviceNames := []string{
		string(ServiceContainerd),
		string(ServiceKubeAPIServer),
		string(ServiceKubeControllerManager),
		string(ServiceKubeScheduler),
		string(ServiceKubeProxy),
		string(ServiceKubelet),
		string(ServiceEtcd),
		string(ServiceK8sAPIServerProxy),
	}

	if err := snap.StopServices(ctx, serviceNames, extraSnapArgs...); err != nil {
		return fmt.Errorf("failed to stop k8s services: %w", err)
	}

	return nil
}
