package snaputil_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/k8sd/pkg/snap/mock"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestGetLocalStatePath(t *testing.T) {
	g := NewWithT(t)

	snap := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: "/var/snap/k8s/common/lock",
		},
	}

	path := snaputil.GetLocalStatePath(snap)
	g.Expect(path).To(Equal("/var/snap/k8s/common/lock/local-state.yaml"))
}

func TestReadWriteLocalState(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	snap := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: tmpDir,
			UID:          os.Getuid(),
			GID:          os.Getgid(),
		},
	}

	// Create a local state
	state := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
		snaputil.ServiceContainerd:            {Enabled: true},
		snaputil.ServiceK8sDqlite:             {Enabled: true},
		snaputil.ServiceEtcd:                  {Enabled: false},
		snaputil.ServiceKubeAPIServer:         {Enabled: false},
		snaputil.ServiceKubeControllerManager: {Enabled: false},
		snaputil.ServiceKubeScheduler:         {Enabled: false},
		snaputil.ServiceKubelet:               {Enabled: true},
		snaputil.ServiceKubeProxy:             {Enabled: false},
		snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
	})

	// Write the state
	err := snaputil.WriteLocalState(snap, state)
	g.Expect(err).ToNot(HaveOccurred())

	// Read the state back
	readState, err := snaputil.ReadLocalState(snap)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(readState.APIVersion).To(Equal(snaputil.LocalNodeStateAPIVersion))
	g.Expect(readState.Kind).To(Equal(snaputil.LocalNodeStateKind))
	g.Expect(readState.Services[snaputil.ServiceContainerd].Enabled).To(BeTrue())
	g.Expect(readState.Services[snaputil.ServiceK8sDqlite].Enabled).To(BeTrue())
	g.Expect(readState.Services[snaputil.ServiceKubelet].Enabled).To(BeTrue())
}

func TestReadLocalStateNotExists(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	snap := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: tmpDir,
		},
	}

	_, err := snaputil.ReadLocalState(snap)
	g.Expect(err).To(HaveOccurred())
	g.Expect(os.IsNotExist(err)).To(BeFalse()) // Error is wrapped
}

func TestDeleteLocalState(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	snap := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: tmpDir,
			UID:          os.Getuid(),
			GID:          os.Getgid(),
		},
	}

	// Create a local state
	state := snaputil.NewControlPlaneLocalState("k8s-dqlite")
	err := snaputil.WriteLocalState(snap, state)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify file exists
	path := snaputil.GetLocalStatePath(snap)
	_, err = os.Stat(path)
	g.Expect(err).ToNot(HaveOccurred())

	// Delete the state
	err = snaputil.DeleteLocalState(snap)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify file is deleted
	_, err = os.Stat(path)
	g.Expect(os.IsNotExist(err)).To(BeTrue())
}

func TestDeleteLocalStateNotExists(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	snap := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: tmpDir,
		},
	}

	// Deleting a non-existent file should not return an error
	err := snaputil.DeleteLocalState(snap)
	g.Expect(err).ToNot(HaveOccurred())
}

func TestEnabledServices(t *testing.T) {
	g := NewWithT(t)

	state := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
		snaputil.ServiceContainerd:            {Enabled: true},
		snaputil.ServiceK8sDqlite:             {Enabled: true},
		snaputil.ServiceKubelet:               {Enabled: true},
		snaputil.ServiceKubeProxy:             {Enabled: false},
		snaputil.ServiceKubeAPIServer:         {Enabled: false},
		snaputil.ServiceKubeControllerManager: {Enabled: false},
		snaputil.ServiceKubeScheduler:         {Enabled: false},
		snaputil.ServiceEtcd:                  {Enabled: false},
		snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
	})

	enabled := state.EnabledServices()
	g.Expect(enabled).To(ContainElements(
		snaputil.ServiceContainerd,
		snaputil.ServiceK8sDqlite,
		snaputil.ServiceKubelet,
	))
	g.Expect(enabled).To(HaveLen(3))
}

func TestSetServiceEnabled(t *testing.T) {
	g := NewWithT(t)

	state := snaputil.NewLocalState(nil)

	state.SetServiceEnabled(snaputil.ServiceContainerd, true)
	state.SetServiceEnabled(snaputil.ServiceK8sDqlite, false)

	g.Expect(state.Services[snaputil.ServiceContainerd].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceK8sDqlite].Enabled).To(BeFalse())
}

func TestNewControlPlaneLocalStateK8sDqlite(t *testing.T) {
	g := NewWithT(t)

	state := snaputil.NewControlPlaneLocalState("k8s-dqlite")

	g.Expect(state.APIVersion).To(Equal(snaputil.LocalNodeStateAPIVersion))
	g.Expect(state.Kind).To(Equal(snaputil.LocalNodeStateKind))
	g.Expect(state.Services[snaputil.ServiceContainerd].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceK8sDqlite].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceEtcd].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceKubeAPIServer].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceKubeControllerManager].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceKubeScheduler].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceKubelet].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceKubeProxy].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceK8sAPIServerProxy].Enabled).To(BeFalse())
}

func TestNewControlPlaneLocalStateEtcd(t *testing.T) {
	g := NewWithT(t)

	state := snaputil.NewControlPlaneLocalState("etcd")

	g.Expect(state.Services[snaputil.ServiceK8sDqlite].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceEtcd].Enabled).To(BeTrue())
}

func TestNewControlPlaneLocalStateExternal(t *testing.T) {
	g := NewWithT(t)

	state := snaputil.NewControlPlaneLocalState("external")

	g.Expect(state.Services[snaputil.ServiceK8sDqlite].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceEtcd].Enabled).To(BeFalse())
}

func TestNewWorkerLocalState(t *testing.T) {
	g := NewWithT(t)

	state := snaputil.NewWorkerLocalState()

	g.Expect(state.APIVersion).To(Equal(snaputil.LocalNodeStateAPIVersion))
	g.Expect(state.Kind).To(Equal(snaputil.LocalNodeStateKind))
	g.Expect(state.Services[snaputil.ServiceContainerd].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceK8sDqlite].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceEtcd].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceKubeAPIServer].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceKubeControllerManager].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceKubeScheduler].Enabled).To(BeFalse())
	g.Expect(state.Services[snaputil.ServiceKubelet].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceKubeProxy].Enabled).To(BeTrue())
	g.Expect(state.Services[snaputil.ServiceK8sAPIServerProxy].Enabled).To(BeTrue())
}

func TestLocalStateYAMLFormat(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()

	snap := &mock.Snap{
		Mock: mock.Mock{
			LockFilesDir: tmpDir,
			UID:          os.Getuid(),
			GID:          os.Getgid(),
		},
	}

	state := snaputil.NewControlPlaneLocalState("k8s-dqlite")
	err := snaputil.WriteLocalState(snap, state)
	g.Expect(err).ToNot(HaveOccurred())

	// Read raw file to verify YAML structure
	content, err := os.ReadFile(filepath.Join(tmpDir, snaputil.LocalNodeStateFileName))
	g.Expect(err).ToNot(HaveOccurred())

	yamlContent := string(content)
	g.Expect(yamlContent).To(ContainSubstring("apiVersion: k8sd.io/v1alpha1"))
	g.Expect(yamlContent).To(ContainSubstring("kind: LocalNodeState"))
	g.Expect(yamlContent).To(ContainSubstring("services:"))
}

func TestStartEnabledServices(t *testing.T) {
	g := NewWithT(t)

	mockSnap := &mock.Snap{
		Mock: mock.Mock{},
	}

	state := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
		snaputil.ServiceContainerd:            {Enabled: true},
		snaputil.ServiceK8sDqlite:             {Enabled: false},
		snaputil.ServiceEtcd:                  {Enabled: false},
		snaputil.ServiceKubeAPIServer:         {Enabled: false},
		snaputil.ServiceKubeControllerManager: {Enabled: false},
		snaputil.ServiceKubeScheduler:         {Enabled: false},
		snaputil.ServiceKubelet:               {Enabled: true},
		snaputil.ServiceKubeProxy:             {Enabled: false},
		snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
	})

	t.Run("Success", func(t *testing.T) {
		mockSnap.StartServicesErr = nil
		mockSnap.StartServicesCalledWith = nil
		err := snaputil.StartEnabledServices(context.Background(), mockSnap, state)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(mockSnap.StartServicesCalledWith[0]).To(ContainElements("containerd", "kubelet"))
		g.Expect(mockSnap.StartServicesCalledWith[0]).ToNot(ContainElement("kube-proxy"))
	})

	t.Run("Failure", func(t *testing.T) {
		mockSnap.StartServicesErr = fmt.Errorf("service start failed")
		err := snaputil.StartEnabledServices(context.Background(), mockSnap, state)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("NoServicesEnabled", func(t *testing.T) {
		emptyState := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
			snaputil.ServiceContainerd:            {Enabled: false},
			snaputil.ServiceK8sDqlite:             {Enabled: false},
			snaputil.ServiceEtcd:                  {Enabled: false},
			snaputil.ServiceKubeAPIServer:         {Enabled: false},
			snaputil.ServiceKubeControllerManager: {Enabled: false},
			snaputil.ServiceKubeScheduler:         {Enabled: false},
			snaputil.ServiceKubelet:               {Enabled: false},
			snaputil.ServiceKubeProxy:             {Enabled: false},
			snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
		})
		mockSnap.StartServicesCalledWith = nil
		err := snaputil.StartEnabledServices(context.Background(), mockSnap, emptyState)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.StartServicesCalledWith).To(BeEmpty())
	})
}

func TestStopEnabledServices(t *testing.T) {
	g := NewWithT(t)

	mockSnap := &mock.Snap{
		Mock: mock.Mock{},
	}

	state := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
		snaputil.ServiceContainerd:            {Enabled: true},
		snaputil.ServiceK8sDqlite:             {Enabled: false},
		snaputil.ServiceEtcd:                  {Enabled: false},
		snaputil.ServiceKubeAPIServer:         {Enabled: false},
		snaputil.ServiceKubeControllerManager: {Enabled: false},
		snaputil.ServiceKubeScheduler:         {Enabled: false},
		snaputil.ServiceKubelet:               {Enabled: true},
		snaputil.ServiceKubeProxy:             {Enabled: false},
		snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
	})

	t.Run("Success", func(t *testing.T) {
		mockSnap.StopServicesErr = nil
		mockSnap.StopServicesCalledWith = nil
		err := snaputil.StopEnabledServices(context.Background(), mockSnap, state)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.StopServicesCalledWith).To(HaveLen(1))
		g.Expect(mockSnap.StopServicesCalledWith[0]).To(ContainElements("containerd", "kubelet"))
		g.Expect(mockSnap.StopServicesCalledWith[0]).ToNot(ContainElement("kube-proxy"))
	})

	t.Run("Failure", func(t *testing.T) {
		mockSnap.StopServicesErr = fmt.Errorf("service stop failed")
		err := snaputil.StopEnabledServices(context.Background(), mockSnap, state)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("NoServicesEnabled", func(t *testing.T) {
		emptyState := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
			snaputil.ServiceContainerd:            {Enabled: false},
			snaputil.ServiceK8sDqlite:             {Enabled: false},
			snaputil.ServiceEtcd:                  {Enabled: false},
			snaputil.ServiceKubeAPIServer:         {Enabled: false},
			snaputil.ServiceKubeControllerManager: {Enabled: false},
			snaputil.ServiceKubeScheduler:         {Enabled: false},
			snaputil.ServiceKubelet:               {Enabled: false},
			snaputil.ServiceKubeProxy:             {Enabled: false},
			snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
		})
		mockSnap.StopServicesCalledWith = nil
		err := snaputil.StopEnabledServices(context.Background(), mockSnap, emptyState)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.StopServicesCalledWith).To(BeEmpty())
	})
}

func TestRestartEnabledServices(t *testing.T) {
	g := NewWithT(t)

	mockSnap := &mock.Snap{
		Mock: mock.Mock{},
	}

	state := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
		snaputil.ServiceContainerd:            {Enabled: true},
		snaputil.ServiceK8sDqlite:             {Enabled: false},
		snaputil.ServiceEtcd:                  {Enabled: false},
		snaputil.ServiceKubeAPIServer:         {Enabled: false},
		snaputil.ServiceKubeControllerManager: {Enabled: false},
		snaputil.ServiceKubeScheduler:         {Enabled: false},
		snaputil.ServiceKubelet:               {Enabled: true},
		snaputil.ServiceKubeProxy:             {Enabled: false},
		snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
	})

	t.Run("Success", func(t *testing.T) {
		mockSnap.RestartServicesErr = nil
		mockSnap.RestartServicesCalledWith = nil
		err := snaputil.RestartEnabledServices(context.Background(), mockSnap, state)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.RestartServicesCalledWith).To(HaveLen(1))
		g.Expect(mockSnap.RestartServicesCalledWith[0]).To(ContainElements("containerd", "kubelet"))
		g.Expect(mockSnap.RestartServicesCalledWith[0]).ToNot(ContainElement("kube-proxy"))
	})

	t.Run("Failure", func(t *testing.T) {
		mockSnap.RestartServicesErr = fmt.Errorf("service restart failed")
		err := snaputil.RestartEnabledServices(context.Background(), mockSnap, state)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("NoServicesEnabled", func(t *testing.T) {
		emptyState := snaputil.NewLocalState(map[snaputil.Service]*snaputil.ServiceState{
			snaputil.ServiceContainerd:            {Enabled: false},
			snaputil.ServiceK8sDqlite:             {Enabled: false},
			snaputil.ServiceEtcd:                  {Enabled: false},
			snaputil.ServiceKubeAPIServer:         {Enabled: false},
			snaputil.ServiceKubeControllerManager: {Enabled: false},
			snaputil.ServiceKubeScheduler:         {Enabled: false},
			snaputil.ServiceKubelet:               {Enabled: false},
			snaputil.ServiceKubeProxy:             {Enabled: false},
			snaputil.ServiceK8sAPIServerProxy:     {Enabled: false},
		})
		mockSnap.RestartServicesCalledWith = nil
		err := snaputil.RestartEnabledServices(context.Background(), mockSnap, emptyState)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.RestartServicesCalledWith).To(BeEmpty())
	})
}

func TestAllK8sServices(t *testing.T) {
	g := NewWithT(t)

	services := snaputil.AllK8sServices()
	g.Expect(services).To(ContainElements(
		snaputil.ServiceContainerd,
		snaputil.ServiceKubeAPIServer,
		snaputil.ServiceKubeControllerManager,
		snaputil.ServiceKubeScheduler,
		snaputil.ServiceKubeProxy,
		snaputil.ServiceKubelet,
		snaputil.ServiceK8sDqlite,
		snaputil.ServiceEtcd,
		snaputil.ServiceK8sAPIServerProxy,
	))
	g.Expect(services).To(HaveLen(9))
}

func TestStopAllK8sServices(t *testing.T) {
	g := NewWithT(t)

	mockSnap := &mock.Snap{
		Mock: mock.Mock{},
	}

	t.Run("Success", func(t *testing.T) {
		mockSnap.StopServicesErr = nil
		mockSnap.StopServicesCalledWith = nil
		err := snaputil.StopAllK8sServices(context.Background(), mockSnap)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mockSnap.StopServicesCalledWith).To(HaveLen(1))
		g.Expect(mockSnap.StopServicesCalledWith[0]).To(ContainElements(
			"containerd",
			"kube-apiserver",
			"kube-controller-manager",
			"kube-scheduler",
			"kube-proxy",
			"kubelet",
			"k8s-dqlite",
			"etcd",
			"k8s-apiserver-proxy",
		))
	})

	t.Run("Failure", func(t *testing.T) {
		mockSnap.StopServicesErr = fmt.Errorf("service stop failed")
		err := snaputil.StopAllK8sServices(context.Background(), mockSnap)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("WithExtraArgs", func(t *testing.T) {
		mockSnap.StopServicesErr = nil
		mockSnap.StopServicesCalledWith = nil
		err := snaputil.StopAllK8sServices(context.Background(), mockSnap, "--no-wait")
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestServiceArgsFromMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]*string
		expected struct {
			updateArgs map[string]string
			deleteArgs []string
		}
	}{
		{
			name:  "NilValue",
			input: map[string]*string{"arg1": nil},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{},
				deleteArgs: []string{"arg1"},
			},
		},
		{
			name:  "EmptyString", // Should be treated as normal string
			input: map[string]*string{"arg1": utils.Pointer("")},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{"arg1": ""},
				deleteArgs: []string{},
			},
		},
		{
			name:  "NonEmptyString",
			input: map[string]*string{"arg1": utils.Pointer("value1")},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{"arg1": "value1"},
				deleteArgs: []string{},
			},
		},
		{
			name: "MixedValues",
			input: map[string]*string{
				"arg1": utils.Pointer("value1"),
				"arg2": utils.Pointer(""),
				"arg3": nil,
			},
			expected: struct {
				updateArgs map[string]string
				deleteArgs []string
			}{
				updateArgs: map[string]string{"arg1": "value1", "arg2": ""},
				deleteArgs: []string{"arg3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			updateArgs, deleteArgs := snaputil.ServiceArgsFromMap(tt.input)
			g.Expect(updateArgs).To(Equal(tt.expected.updateArgs))
			g.Expect(deleteArgs).To(Equal(tt.expected.deleteArgs))
		})
	}
}
