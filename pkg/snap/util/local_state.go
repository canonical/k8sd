package snaputil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	ServiceK8sDqlite             Service = "k8s-dqlite"
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

// GetLocalStatePath returns the path to the local state file.
func GetLocalStatePath(snap snap.Snap) string {
	return filepath.Join(snap.LockFilesDir(), LocalNodeStateFileName)
}

// ReadLocalState reads the local state file.
func ReadLocalState(snap snap.Snap) (*LocalNodeState, error) {
	path := GetLocalStatePath(snap)
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
	path := GetLocalStatePath(snap)

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
	path := GetLocalStatePath(snap)
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
	var k8sDqliteEnabled, etcdEnabled bool

	switch datastoreType {
	case "k8s-dqlite":
		k8sDqliteEnabled = true
		etcdEnabled = false
	case "etcd":
		k8sDqliteEnabled = false
		etcdEnabled = true
	case "external":
		k8sDqliteEnabled = false
		etcdEnabled = false
	}

	services := map[Service]*ServiceState{
		ServiceContainerd:            {Enabled: true},
		ServiceK8sDqlite:             {Enabled: k8sDqliteEnabled},
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
		ServiceK8sDqlite:             {Enabled: false},
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

// StopEnabledServices stops all services that are enabled in the local state.
func StopEnabledServices(ctx context.Context, snap snap.Snap, state *LocalNodeState, extraSnapArgs ...string) error {
	services := state.EnabledServices()
	if len(services) == 0 {
		return nil
	}

	// Convert Service enum to strings for snap.StopServices
	serviceNames := make([]string, len(services))
	for i, service := range services {
		serviceNames[i] = string(service)
	}

	if err := snap.StopServices(ctx, serviceNames, extraSnapArgs...); err != nil {
		return fmt.Errorf("failed to stop services: %w", err)
	}

	return nil
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

// allK8sServices contains all k8s services except k8sd.
// This is used for cleanup operations where we want to stop all services.
var allK8sServices = []Service{
	ServiceContainerd,
	ServiceKubeAPIServer,
	ServiceKubeControllerManager,
	ServiceKubeScheduler,
	ServiceKubeProxy,
	ServiceKubelet,
	ServiceK8sDqlite,
	ServiceEtcd,
	ServiceK8sAPIServerProxy,
}

// AllK8sServices returns a list of all K8s service names (for cleanup operations).
func AllK8sServices() []Service {
	return allK8sServices
}

// StopAllK8sServices stops all K8s services except k8sd (for cleanup/remove operations).
func StopAllK8sServices(ctx context.Context, snap snap.Snap, extraSnapArgs ...string) error {
	serviceNames := make([]string, len(allK8sServices))
	for i, service := range allK8sServices {
		serviceNames[i] = string(service)
	}

	if err := snap.StopServices(ctx, serviceNames, extraSnapArgs...); err != nil {
		return fmt.Errorf("failed to stop k8s services: %w", err)
	}

	return nil
}

// ServiceArgsFromMap processes a map of string pointers and categorizes them into update and delete lists.
// - If the value pointer is nil, it adds the argument name to the delete list.
// - If the value pointer is not nil, it adds the argument and its value to the update map.
func ServiceArgsFromMap(args map[string]*string) (map[string]string, []string) {
	updateArgs := make(map[string]string)
	deleteArgs := make([]string, 0)

	for arg, val := range args {
		if val == nil {
			deleteArgs = append(deleteArgs, arg)
		} else {
			updateArgs[arg] = *val
		}
	}
	return updateArgs, deleteArgs
}
