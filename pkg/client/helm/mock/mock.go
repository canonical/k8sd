package mock

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/canonical/k8sd/pkg/client/helm"
)

type MockApplyArguments struct {
	Context context.Context
	Chart   helm.InstallableChart
	State   helm.State
	Values  map[string]any
}

type MockGetDeployedConfigArguments struct {
	Context context.Context
	Chart   helm.InstallableChart
}

type MockGetLastDeployedArguments struct {
	Context context.Context
	Chart   helm.InstallableChart
}

// Mock is a mock implementation of helm.Client.
type Mock struct {
	mu              sync.Mutex
	ApplyCalledWith []MockApplyArguments
	ApplyChanged    bool
	ApplyErr        error

	GetDeployedConfigCalledWith []MockGetDeployedConfigArguments
	GetDeployedConfigResult     map[string]any
	GetDeployedConfigDeployed   bool
	GetDeployedConfigErr        error
	GetDeployedConfigFunc       func(ctx context.Context, c helm.InstallableChart) (map[string]any, bool, error)

	GetLastDeployedCalledWith []MockGetLastDeployedArguments
	GetLastDeployedResult     time.Time
	GetLastDeployedDeployed   bool
	GetLastDeployedErr        error
	GetLastDeployedFunc       func(ctx context.Context, c helm.InstallableChart) (time.Time, bool, error)
}

// Apply implements helm.Client.
func (m *Mock) Apply(ctx context.Context, c helm.InstallableChart, desired helm.State, values map[string]any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ApplyCalledWith = append(m.ApplyCalledWith, MockApplyArguments{Context: ctx, Chart: c, State: desired, Values: values})
	return m.ApplyChanged, m.ApplyErr
}

// Calls returns a snapshot of recorded Apply invocations safe for concurrent inspection.
func (m *Mock) Calls() []MockApplyArguments {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.ApplyCalledWith)
}

// GetDeployedConfig implements helm.Client.
func (m *Mock) GetDeployedConfig(ctx context.Context, c helm.InstallableChart) (map[string]any, bool, error) {
	m.mu.Lock()
	m.GetDeployedConfigCalledWith = append(m.GetDeployedConfigCalledWith, MockGetDeployedConfigArguments{Context: ctx, Chart: c})
	fn := m.GetDeployedConfigFunc
	res := m.GetDeployedConfigResult
	dep := m.GetDeployedConfigDeployed
	err := m.GetDeployedConfigErr
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, c)
	}

	if dep && res == nil {
		return map[string]any{}, true, err
	}
	if res != nil {
		b, marshalErr := json.Marshal(res)
		if marshalErr != nil {
			return nil, dep, marshalErr
		}
		var cloned map[string]any
		if unmarshalErr := json.Unmarshal(b, &cloned); unmarshalErr != nil {
			return nil, dep, unmarshalErr
		}
		return cloned, dep, err
	}
	return nil, dep, err
}

// GetDeployedConfigCalls returns a snapshot of recorded GetDeployedConfig invocations safe for concurrent inspection.
func (m *Mock) GetDeployedConfigCalls() []MockGetDeployedConfigArguments {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.GetDeployedConfigCalledWith)
}

// GetLastDeployed implements helm.Client.
func (m *Mock) GetLastDeployed(ctx context.Context, c helm.InstallableChart) (time.Time, bool, error) {
	m.mu.Lock()
	m.GetLastDeployedCalledWith = append(m.GetLastDeployedCalledWith, MockGetLastDeployedArguments{Context: ctx, Chart: c})
	fn := m.GetLastDeployedFunc
	res := m.GetLastDeployedResult
	dep := m.GetLastDeployedDeployed
	err := m.GetLastDeployedErr
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, c)
	}

	return res, dep, err
}

// GetLastDeployedCalls returns a snapshot of recorded GetLastDeployed invocations safe for concurrent inspection.
func (m *Mock) GetLastDeployedCalls() []MockGetLastDeployedArguments {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.GetLastDeployedCalledWith)
}

var _ helm.Client = &Mock{}
