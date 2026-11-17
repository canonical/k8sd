package controllers_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/controllers"
	"github.com/canonical/k8sd/pkg/k8sd/setup"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
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

	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
		},
	)
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

	ctrl := controllers.NewNodeConfigurationController(s, func() {}, 2*time.Minute)

	keyCh := make(chan *rsa.PublicKey)

	go ctrl.Run(ctx, func(ctx context.Context) (*rsa.PublicKey, error) { return <-keyCh, nil },
		func(ctx context.Context, b bool) error {
			return nil
		})
	defer watcher.Stop()

	// WatchConfigMap now does an initial Get to seed the reconcile. Drain that
	// seed reconcile (empty configmap, nil key → no-op) before the test loop.
	keyCh <- nil
	select {
	case <-ctrl.ReconciledCh():
	case <-time.After(channelSendTimeout):
		g.Fail("Timed out waiting for seed reconcile to complete")
	}

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

func TestKubeProxyEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := NewWithT(t)

	tmpDir := t.TempDir()

	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
		},
	)
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

	removeKubeProxyCalled := make(chan struct{}, 100)

	ctrl := controllers.NewNodeConfigurationController(s, func() {}, 2*time.Minute).
		WithRemoveKubeProxyRules(func(ctx context.Context, s snap.Snap) error {
			removeKubeProxyCalled <- struct{}{}
			return nil
		})

	keyCh := make(chan *rsa.PublicKey)

	updateKubeProxyCalled := make(chan bool, 100)
	go ctrl.Run(ctx, func(ctx context.Context) (*rsa.PublicKey, error) { return <-keyCh, nil },
		func(ctx context.Context, b bool) error {
			updateKubeProxyCalled <- b
			return nil
		})
	defer watcher.Stop()

	// WatchConfigMap now does an initial Get to seed the reconcile. Drain that
	// seed reconcile (empty configmap, nil key → no-op) before the test loop.
	keyCh <- nil
	select {
	case <-ctrl.ReconciledCh():
	case <-time.After(channelSendTimeout):
		g.Fail("Timed out waiting for seed reconcile to complete")
	}

	t.Run("DisableKubeProxy", func(t *testing.T) {
		g := NewWithT(t)
		s.StopServicesCalledWith = nil
		for len(updateKubeProxyCalled) > 0 {
			<-updateKubeProxyCalled
		}

		// Send configmap with kube-proxy-enabled set to false
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-enabled": "false",
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
		g.Expect(updateKubeProxyCalled).To(Receive(BeFalse()))
		g.Expect(removeKubeProxyCalled).To(Receive())
		g.Expect(removeKubeProxyCalled).ToNot(Receive())
	})

	t.Run("EnableKubeProxy", func(t *testing.T) {
		g := NewWithT(t)
		s.StartServicesCalledWith = nil
		for len(updateKubeProxyCalled) > 0 {
			<-updateKubeProxyCalled
		}

		// Send configmap with kube-proxy-enabled set to true
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-enabled": "true",
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
		g.Expect(updateKubeProxyCalled).To(Receive(BeTrue()))
		g.Expect(removeKubeProxyCalled).ToNot(Receive())
	})

	// NOTE: these subtests share the controller goroutine, watcher, keyCh, and snap mock.
	// They are inherently sequential and must NOT be run with t.Parallel().
	t.Run("ChangeWhenStateMatches", func(t *testing.T) {
		// When the config map changes the reconciler will try to start or stop kubeproxy
		// based on the config. Systemd service start/start will be a noop if the service
		// is already in the right state.
		// in this test we perform a series of config updates and make sure the service
		// start/stop calls match the requested state.
		g := NewWithT(t)
		s.StartServicesCalledWith = nil
		s.StopServicesCalledWith = nil
		for len(updateKubeProxyCalled) > 0 {
			<-updateKubeProxyCalled
		}

		// Send configmap with kube-proxy-enabled set to true (matching current state)
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-enabled": "true",
			},
		}

		watcher.Add(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}

		// Verify only one start was called
		g.Expect(s.StartServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(s.StartServicesCalledWith[0]).To(ContainElement("kube-proxy"))
		g.Expect(s.StopServicesCalledWith).To(BeEmpty())
		g.Expect(updateKubeProxyCalled).To(Receive(BeTrue()))
		g.Expect(updateKubeProxyCalled).ToNot(Receive())
		g.Expect(removeKubeProxyCalled).ToNot(Receive())
		watcher.Modify(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}
		g.Expect(s.StartServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StartServicesCalledWith).To(HaveLen(2))
		g.Expect(s.StartServicesCalledWith[0]).To(ContainElement("kube-proxy"))
		g.Expect(s.StartServicesCalledWith[1]).To(ContainElement("kube-proxy"))
		g.Expect(s.StopServicesCalledWith).To(BeEmpty())
		g.Expect(updateKubeProxyCalled).To(Receive(BeTrue()))
		g.Expect(updateKubeProxyCalled).ToNot(Receive())
		g.Expect(removeKubeProxyCalled).ToNot(Receive())
		configmap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-enabled": "false",
			},
		}
		watcher.Modify(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}
		g.Expect(s.StartServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StartServicesCalledWith).To(HaveLen(2))
		g.Expect(s.StartServicesCalledWith[0]).To(ContainElement("kube-proxy"))
		g.Expect(s.StartServicesCalledWith[1]).To(ContainElement("kube-proxy"))
		g.Expect(s.StopServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StopServicesCalledWith).To(HaveLen(1))
		g.Expect(s.StopServicesCalledWith[0]).To(ContainElement("kube-proxy"))
		g.Expect(updateKubeProxyCalled).To(Receive(BeFalse()))
		g.Expect(updateKubeProxyCalled).ToNot(Receive())
		g.Expect(removeKubeProxyCalled).To(Receive())
		g.Expect(removeKubeProxyCalled).ToNot(Receive())
	})

	t.Run("SteadyStateDisabledDoesNotCleanUpRules", func(t *testing.T) {
		g := NewWithT(t)
		s.StopServicesCalledWith = nil
		for len(updateKubeProxyCalled) > 0 {
			<-updateKubeProxyCalled
		}
		for len(removeKubeProxyCalled) > 0 {
			<-removeKubeProxyCalled
		}

		// Send configmap with kube-proxy-enabled still set to false (steady state)
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data: map[string]string{
				"kube-proxy-enabled": "false",
			},
		}

		watcher.Modify(configmap)
		keyCh <- nil

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}

		// Verify kube-proxy stop was called
		g.Expect(s.StopServicesCalledWith).ToNot(BeEmpty())
		g.Expect(s.StopServicesCalledWith[0]).To(ContainElement("kube-proxy"))
		g.Expect(updateKubeProxyCalled).To(Receive(BeFalse()))
		g.Expect(updateKubeProxyCalled).ToNot(Receive())

		// Verify RemoveKubeProxyRules was NOT called because kube-proxy was already disabled
		g.Expect(removeKubeProxyCalled).ToNot(Receive())
	})
}

func TestKubeProxyCleanupRuleTransitions(t *testing.T) {
	t.Run("MultiNodeIndependentCleanup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := NewWithT(t)

		var (
			dbMu           sync.Mutex
			sharedDBEnable = true
		)
		sharedUpdate := func(_ context.Context, enabled bool) error {
			dbMu.Lock()
			defer dbMu.Unlock()
			sharedDBEnable = enabled
			return nil
		}

		setupNode := func() (*watch.FakeWatcher, chan *rsa.PublicKey, *controllers.NodeConfigurationController, chan struct{}) {
			dir := t.TempDir()
			cs := fake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			})
			w := watch.NewFake()
			cs.PrependWatchReactor("configmaps", k8stesting.DefaultWatchReactor(w, nil))
			s := &mock.Snap{
				Mock: mock.Mock{
					ServiceArgumentsDir:  filepath.Join(dir, "args"),
					LockFilesDir:         filepath.Join(dir, "lock"),
					UID:                  os.Getuid(),
					GID:                  os.Getgid(),
					KubernetesNodeClient: &kubernetes.Client{Interface: cs},
				},
			}
			g.Expect(setup.EnsureAllDirectories(s)).To(Succeed())
			cleanupCh := make(chan struct{}, 10)
			ctrl := controllers.NewNodeConfigurationController(s, func() {}, 2*time.Minute).
				WithRemoveKubeProxyRules(func(context.Context, snap.Snap) error {
					cleanupCh <- struct{}{}
					return nil
				})
			kCh := make(chan *rsa.PublicKey)
			go ctrl.Run(ctx, func(context.Context) (*rsa.PublicKey, error) { return <-kCh, nil }, sharedUpdate)
			// Drain seed reconcile (empty configmap -> kube-proxy enabled).
			kCh <- nil
			select {
			case <-ctrl.ReconciledCh():
			case <-time.After(channelSendTimeout):
				g.Fail("Timed out waiting for seed reconcile")
			}
			return w, kCh, ctrl, cleanupCh
		}

		w1, k1, c1, clean1 := setupNode()
		defer w1.Stop()
		w2, k2, c2, clean2 := setupNode()
		defer w2.Stop()

		disableCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data:       map[string]string{"kube-proxy-enabled": "false"},
		}

		// Node 1 reconciles first and updates shared DB to false.
		w1.Modify(disableCM)
		k1 <- nil
		select {
		case <-c1.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for node 1 reconcile")
		}
		g.Expect(clean1).To(Receive())

		// Node 2 reconciles second; even though shared DB is already false, node 2 must still clean its local rules.
		w2.Modify(disableCM)
		k2 <- nil
		select {
		case <-c2.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for node 2 reconcile")
		}
		g.Expect(clean2).To(Receive())

		dbMu.Lock()
		g.Expect(sharedDBEnable).To(BeFalse())
		dbMu.Unlock()
	})

	t.Run("CleanupFailureRetriesUntilSuccess", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := NewWithT(t)

		dir := t.TempDir()
		cs := fake.NewSimpleClientset(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
		})
		w := watch.NewFake()
		cs.PrependWatchReactor("configmaps", k8stesting.DefaultWatchReactor(w, nil))
		defer w.Stop()

		s := &mock.Snap{
			Mock: mock.Mock{
				ServiceArgumentsDir:  filepath.Join(dir, "args"),
				LockFilesDir:         filepath.Join(dir, "lock"),
				UID:                  os.Getuid(),
				GID:                  os.Getgid(),
				KubernetesNodeClient: &kubernetes.Client{Interface: cs},
			},
		}
		g.Expect(setup.EnsureAllDirectories(s)).To(Succeed())

		var (
			mu          sync.Mutex
			failCleanup = true
		)
		cleanupCh := make(chan struct{}, 10)
		updateCh := make(chan bool, 10)

		ctrl := controllers.NewNodeConfigurationController(s, func() {}, 2*time.Minute).
			WithRemoveKubeProxyRules(func(context.Context, snap.Snap) error {
				cleanupCh <- struct{}{}
				mu.Lock()
				defer mu.Unlock()
				if failCleanup {
					return errors.New("xtables lock busy")
				}
				return nil
			})

		keyCh := make(chan *rsa.PublicKey)
		go ctrl.Run(ctx, func(context.Context) (*rsa.PublicKey, error) { return <-keyCh, nil },
			func(_ context.Context, enabled bool) error {
				updateCh <- enabled
				return nil
			})

		// Drain seed reconcile.
		keyCh <- nil
		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for seed reconcile")
		}
		g.Expect(updateCh).To(Receive(BeTrue()))

		disableCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data:       map[string]string{"kube-proxy-enabled": "false"},
		}

		// First disable reconcile fails during rule cleanup: marker and DB state must not commit.
		w.Modify(disableCM)
		keyCh <- nil
		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for failed cleanup reconcile")
		}
		g.Expect(cleanupCh).To(Receive())
		g.Expect(updateCh).ToNot(Receive())
		disabled, err := snaputil.IsKubeProxyDisabled(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(disabled).To(BeFalse())

		// Allow cleanup to succeed on the next reconcile and verify both cleanup and DB update run.
		mu.Lock()
		failCleanup = false
		mu.Unlock()
		w.Modify(disableCM)
		keyCh <- nil
		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for retry reconcile")
		}
		g.Expect(cleanupCh).To(Receive())
		g.Expect(updateCh).To(Receive(BeFalse()))
		disabled, err = snaputil.IsKubeProxyDisabled(s)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(disabled).To(BeTrue())

		// Subsequent reconcile with kube-proxy still disabled skips cleanup.
		w.Modify(disableCM)
		keyCh <- nil
		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for steady-state reconcile")
		}
		g.Expect(cleanupCh).ToNot(Receive())
	})

	t.Run("PreMarkedDisabledSkipsCleanup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := NewWithT(t)

		dir := t.TempDir()
		disableCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "k8sd-config", Namespace: "kube-system"},
			Data:       map[string]string{"kube-proxy-enabled": "false"},
		}
		cs := fake.NewSimpleClientset(disableCM)
		w := watch.NewFake()
		cs.PrependWatchReactor("configmaps", k8stesting.DefaultWatchReactor(w, nil))
		defer w.Stop()

		s := &mock.Snap{
			Mock: mock.Mock{
				ServiceArgumentsDir:  filepath.Join(dir, "args"),
				LockFilesDir:         filepath.Join(dir, "lock"),
				UID:                  os.Getuid(),
				GID:                  os.Getgid(),
				KubernetesNodeClient: &kubernetes.Client{Interface: cs},
			},
		}
		g.Expect(setup.EnsureAllDirectories(s)).To(Succeed())
		g.Expect(snaputil.MarkKubeProxyDisabled(s, true)).To(Succeed())

		cleanupCh := make(chan struct{}, 10)
		ctrl := controllers.NewNodeConfigurationController(s, func() {}, 2*time.Minute).
			WithRemoveKubeProxyRules(func(context.Context, snap.Snap) error {
				cleanupCh <- struct{}{}
				return nil
			})
		keyCh := make(chan *rsa.PublicKey)
		go ctrl.Run(ctx, func(context.Context) (*rsa.PublicKey, error) { return <-keyCh, nil },
			func(context.Context, bool) error { return nil })

		keyCh <- nil
		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for reconcile")
		}
		g.Expect(cleanupCh).ToNot(Receive())

		// Restarting kube-proxy clears the disabled marker so the next reconcile cleans up rules again.
		g.Expect(s.RestartServices(ctx, []string{"kube-proxy"})).To(Succeed())
		w.Modify(disableCM)
		keyCh <- nil
		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Timed out waiting for post-restart reconcile")
		}
		g.Expect(cleanupCh).To(Receive())
	})
}
