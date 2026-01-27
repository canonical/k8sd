package controllers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/k8sd/controllers"
	"github.com/canonical/k8sd/pkg/k8sd/setup"
	"github.com/canonical/k8sd/pkg/snap/mock"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func setupTestEnv(t *testing.T) (*mock.Snap, *watch.FakeWatcher, string, string) {
	clientset := fake.NewSimpleClientset()
	watcher := watch.NewFake()
	clientset.PrependWatchReactor("nodes", k8stesting.DefaultWatchReactor(watcher, nil))

	s := &mock.Snap{
		Mock: mock.Mock{
			K8sdStateDir:         filepath.Join(t.TempDir(), "k8sd"),
			UID:                  os.Getuid(),
			GID:                  os.Getgid(),
			KubernetesNodeClient: &kubernetes.Client{Interface: clientset},
		},
	}

	g := NewWithT(t)
	g.Expect(setup.EnsureAllDirectories(s)).To(Succeed())

	k8sdDbDir := filepath.Join(s.K8sdStateDir(), "database")
	g.Expect(os.MkdirAll(k8sdDbDir, 0o700)).To(Succeed())

	nodeName := "test-node-name"
	return s, watcher, k8sdDbDir, nodeName
}

func TestAvailabilityZoneLabel(t *testing.T) {
	t.Run("Ensure failure domain is only set for k8sd with etcd datastore", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		g := NewWithT(t)

		s, watcher, k8sdDbDir, nodeName := setupTestEnv(t)
		ctrl := controllers.NewNodeLabelController(
			s,
			func() {},
			func(context.Context) (string, error) { return nodeName, nil },
		)

		go ctrl.Run(ctx, func(context.Context) (string, error) { return "etcd", nil })
		defer watcher.Stop()

		expFailureDomain := uint64(7130520900010879344)

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					"topology.kubernetes.io/zone": "testAZ",
				},
			},
		}
		watcher.Add(node)

		select {
		case <-ctrl.ReconciledCh():
		case <-time.After(channelSendTimeout):
			g.Fail("Time out while waiting for the reconcile to complete")
		}

		k8sdFailureDomain, err := snaputil.GetDqliteFailureDomain(k8sdDbDir)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(k8sdFailureDomain).To(Equal(expFailureDomain))

		g.Expect(s.RestartServicesCalledWith).To(ContainElement([]string{"k8sd"}))
	})
}
