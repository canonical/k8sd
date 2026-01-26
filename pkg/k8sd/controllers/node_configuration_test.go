package controllers_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/controllers"
	"github.com/canonical/k8sd/pkg/k8sd/setup"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap/mock"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestConfigPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := NewWithT(t)

	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	g.Expect(err).To(Not(HaveOccurred()))

	wrongPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	g.Expect(err).To(Not(HaveOccurred()))

	tests := []struct {
		name          string
		configmap     *corev1.ConfigMap
		expectArgs    map[string]string
		expectRestart bool
		privKey       *rsa.PrivateKey
		pubKey        *rsa.PublicKey
	}{
		{
			name: "Initial",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-dns":    "10.152.1.1",
					"cluster-domain": "test-cluster.local",
					"cloud-provider": "provider",
				},
			},
			expectArgs: map[string]string{
				"--cluster-dns":    "10.152.1.1",
				"--cluster-domain": "test-cluster.local",
				"--cloud-provider": "provider",
			},
			expectRestart: true,
		},
		{
			name: "IgnoreUnknownFields",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"non-existent-key1": "value1",
					"non-existent-key2": "value2",
					"non-existent-key3": "value3",
				},
			},
			expectArgs: map[string]string{
				"--cluster-dns":    "10.152.1.1",
				"--cluster-domain": "test-cluster.local",
				"--cloud-provider": "provider",
			},
		},
		{
			name: "RemoveClusterDNS",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-dns": "",
				},
			},
			expectArgs: map[string]string{
				"--cluster-dns":    "",
				"--cluster-domain": "test-cluster.local",
				"--cloud-provider": "provider",
			},
			expectRestart: true,
		},
		{
			name: "UpdateDNS",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-domain": "test-cluster2.local",
					"cluster-dns":    "10.152.1.3",
				},
			},
			expectArgs: map[string]string{
				"--cluster-domain": "test-cluster2.local",
				"--cluster-dns":    "10.152.1.3",
				"--cloud-provider": "provider",
			},
			expectRestart: true,
		},
		{
			name: "PreserveClusterDomain",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-dns": "10.152.1.3",
				},
			},
			expectArgs: map[string]string{
				"--cluster-domain": "test-cluster2.local",
				"--cluster-dns":    "10.152.1.3",
				"--cloud-provider": "provider",
			},
		},
		{
			name: "WithSignature",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-dns":    "10.152.1.1",
					"cluster-domain": "test-cluster.local",
					"cloud-provider": "provider",
				},
			},
			expectArgs: map[string]string{
				"--cluster-dns":    "10.152.1.1",
				"--cluster-domain": "test-cluster.local",
				"--cloud-provider": "provider",
			},
			privKey:       privKey,
			pubKey:        &privKey.PublicKey,
			expectRestart: true,
		},
		{
			name: "MissingPrivKey",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-dns":    "10.152.1.1",
					"cluster-domain": "test-cluster2.local",
					"cloud-provider": "provider",
				},
			},
			expectArgs: map[string]string{
				"--cluster-dns":    "10.152.1.1",
				"--cluster-domain": "test-cluster.local",
				"--cloud-provider": "provider",
			},
			pubKey:        &privKey.PublicKey,
			expectRestart: false,
		},
		{
			name: "InvalidSignature",
			configmap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
				Data: map[string]string{
					"cluster-dns":    "10.152.1.1",
					"cluster-domain": "test-cluster2.local",
					"cloud-provider": "provider",
				},
			},
			expectArgs: map[string]string{
				"--cluster-dns":    "10.152.1.1",
				"--cluster-domain": "test-cluster.local",
				"--cloud-provider": "provider",
			},
			privKey:       wrongPrivKey,
			pubKey:        &privKey.PublicKey,
			expectRestart: false,
		},
	}

	tmpDir := t.TempDir()

	clientset := fake.NewSimpleClientset()
	watcher := watch.NewFake()
	clientset.PrependWatchReactor("configmaps", k8stesting.DefaultWatchReactor(watcher, nil))

	s := &mock.Snap{
		Mock: mock.Mock{
			ServiceArgumentsDir:  filepath.Join(tmpDir, "args"),
			LockFilesDir:         tmpDir,
			UID:                  os.Getuid(),
			GID:                  os.Getgid(),
			KubernetesNodeClient: &kubernetes.Client{Interface: clientset},
		},
	}

	g.Expect(setup.EnsureAllDirectories(s)).To(Succeed())

	initialState := snaputil.NewControlPlaneLocalState("etcd")
	g.Expect(snaputil.WriteLocalState(s, initialState)).To(Succeed())

	ctrl := controllers.NewNodeConfigurationController(s, func() {})

	keyCh := make(chan *rsa.PublicKey)

	go ctrl.Run(ctx, func(ctx context.Context) (*rsa.PublicKey, error) { return <-keyCh, nil })
	defer watcher.Stop()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s.RestartServicesCalledWith = nil

			g := NewWithT(t)

			if tc.privKey != nil {
				// Create ClusterConfig from test configmap data for signing
				config, err := types.ConfigMapToClusterConfig(tc.configmap.Data, nil)
				g.Expect(err).To(Not(HaveOccurred()))

				tc.configmap.Data, err = types.ClusterConfigToConfigMap(config, tc.privKey)
				g.Expect(err).To(Not(HaveOccurred()))
			}

			watcher.Add(tc.configmap)

			keyCh <- tc.pubKey

			select {
			case <-ctrl.ReconciledCh():
			case <-time.After(channelSendTimeout):
				g.Fail("Time out while waiting for the reconcile to complete")
			}

			for ekey, evalue := range tc.expectArgs {
				val, err := snaputil.GetServiceArgument(s, "kubelet", ekey)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(val).To(Equal(evalue))
			}

			if tc.expectRestart {
				g.Expect(s.RestartServicesCalledWith[0]).To(Equal([]string{"kubelet"}))
			} else {
				g.Expect(s.RestartServicesCalledWith).To(BeEmpty())
			}
		})
	}
}

func TestKubeProxyFree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := NewWithT(t)

	tmpDir := t.TempDir()

	clientset := fake.NewSimpleClientset()
	watcher := watch.NewFake()
	clientset.PrependWatchReactor("configmaps", k8stesting.DefaultWatchReactor(watcher, nil))

	s := &mock.Snap{
		Mock: mock.Mock{
			ServiceArgumentsDir:  filepath.Join(tmpDir, "args"),
			LockFilesDir:         tmpDir,
			UID:                  os.Getuid(),
			GID:                  os.Getgid(),
			KubernetesNodeClient: &kubernetes.Client{Interface: clientset},
		},
	}

	g.Expect(setup.EnsureAllDirectories(s)).To(Succeed())

	// Write initial local state with kube-proxy enabled
	initialState := snaputil.NewControlPlaneLocalState("k8s-dqlite")
	g.Expect(initialState.Services[snaputil.ServiceKubeProxy].Enabled).To(BeTrue())
	err := snaputil.WriteLocalState(s, initialState)
	g.Expect(err).ToNot(HaveOccurred())

	ctrl := controllers.NewNodeConfigurationController(s, func() {})

	keyCh := make(chan *rsa.PublicKey)

	go ctrl.Run(ctx, func(ctx context.Context) (*rsa.PublicKey, error) { return <-keyCh, nil })
	defer watcher.Stop()

	t.Run("DisableKubeProxy", func(t *testing.T) {
		g := NewWithT(t)
		s.StopServicesCalledWith = nil

		// Send configmap with kube-proxy-free enabled
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-free": "true",
			},
		}

		watcher.Add(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}

		// Verify kube-proxy was stopped
		g.Expect(s.StopServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StopServicesCalledWith[0]).To(ContainElement("kube-proxy"))

		// Verify local state was updated
		localState, err := snaputil.ReadLocalState(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(localState.Services[snaputil.ServiceKubeProxy].Enabled).To(BeFalse())
	})

	t.Run("EnableKubeProxy", func(t *testing.T) {
		g := NewWithT(t)
		s.StartServicesCalledWith = nil

		// Send configmap with kube-proxy-free disabled
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-free": "false",
			},
		}

		watcher.Add(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}

		// Verify kube-proxy was started
		g.Expect(s.StartServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StartServicesCalledWith[0]).To(ContainElement("kube-proxy"))

		// Verify local state was updated
		localState, err := snaputil.ReadLocalState(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(localState.Services[snaputil.ServiceKubeProxy].Enabled).To(BeTrue())
	})

	t.Run("NoChangeWhenStateMatches", func(t *testing.T) {
		g := NewWithT(t)
		s.StartServicesCalledWith = nil
		s.StopServicesCalledWith = nil

		// Local state already has kube-proxy enabled (from previous test)
		// Send configmap with kube-proxy-free disabled (matching current state)
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-free": "false",
			},
		}

		watcher.Add(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}

		// Verify no service operations were performed
		g.Expect(s.StartServicesCalledWith).To(BeEmpty())
		g.Expect(s.StopServicesCalledWith).To(BeEmpty())
	})
}
