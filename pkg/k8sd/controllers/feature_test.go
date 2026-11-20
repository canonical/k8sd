package controllers_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations"
	"github.com/canonical/k8sd/pkg/k8sd/controllers"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
)

type fakeFeatureImpl struct {
	features.Interface
	applyDNSFunc func(context.Context, snap.Snap, types.DNS, types.Kubelet, types.Annotations) (types.FeatureStatus, string, error)
}

func (f *fakeFeatureImpl) ApplyDNS(ctx context.Context, s snap.Snap, d types.DNS, k types.Kubelet, a types.Annotations) (types.FeatureStatus, string, error) {
	if f.applyDNSFunc != nil {
		return f.applyDNSFunc(ctx, s, d, k, a)
	}
	return types.FeatureStatus{}, "", nil
}

func TestFeatureController_DNS_Notify(t *testing.T) {
	t.Run("NotifiesWhenDNSIPIsNonEmpty", func(t *testing.T) {
		g := NewWithT(t)

		const dnsIP = "10.152.183.211"
		fakeImpl := &fakeFeatureImpl{
			applyDNSFunc: func(ctx context.Context, s snap.Snap, d types.DNS, k types.Kubelet, a types.Annotations) (types.FeatureStatus, string, error) {
				return types.FeatureStatus{Enabled: true}, dnsIP, nil
			},
		}

		var notifiedIP atomic.Pointer[string]
		notifyDNSChangedIP := func(ctx context.Context, ip string) error {
			notifiedIP.Store(&ip)
			return nil
		}

		triggerDNSCh := make(chan struct{}, 1)
		snapM := &snapmock.Snap{}
		ctrl := controllers.NewFeatureController(controllers.FeatureControllerOpts{
			Snap:         snapM,
			Features:     fakeImpl,
			WaitReady:    func() {},
			TriggerDNSCh: triggerDNSCh,
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		getClusterConfig := func(context.Context) (types.ClusterConfig, error) {
			return types.ClusterConfig{
				Kubelet: types.Kubelet{
					ClusterDNS: ptr.To(dnsIP),
				},
				Annotations: types.Annotations{
					apiv1_annotations.AnnotationDisableSeparateFeatureUpgrades: "true",
				},
			}, nil
		}
		getState := func() mctypes.State { return nil }
		setFeatureStatus := func(ctx context.Context, name types.FeatureName, featureStatus types.FeatureStatus) error {
			return nil
		}

		ctrl.Run(ctx, getClusterConfig, getState, notifyDNSChangedIP, setFeatureStatus)
		<-ctrl.ReadyCh()

		triggerDNSCh <- struct{}{}

		select {
		case <-ctrl.ReconciledDNSCh():
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for DNS reconcile")
		}

		g.Expect(notifiedIP.Load()).NotTo(BeNil())
		g.Expect(*notifiedIP.Load()).To(Equal(dnsIP), "notifyDNSChangedIP must be called when dnsIP is non-empty")
	})

	t.Run("DoesNotNotifyWhenDNSIPIsEmpty", func(t *testing.T) {
		g := NewWithT(t)

		fakeImpl := &fakeFeatureImpl{
			applyDNSFunc: func(ctx context.Context, s snap.Snap, d types.DNS, k types.Kubelet, a types.Annotations) (types.FeatureStatus, string, error) {
				return types.FeatureStatus{Enabled: false}, "", nil
			},
		}

		var notifyCount atomic.Int32
		notifyDNSChangedIP := func(ctx context.Context, dnsIP string) error {
			notifyCount.Add(1)
			return nil
		}

		triggerDNSCh := make(chan struct{}, 1)
		snapM := &snapmock.Snap{}
		ctrl := controllers.NewFeatureController(controllers.FeatureControllerOpts{
			Snap:         snapM,
			Features:     fakeImpl,
			WaitReady:    func() {},
			TriggerDNSCh: triggerDNSCh,
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		getClusterConfig := func(context.Context) (types.ClusterConfig, error) {
			return types.ClusterConfig{
				Annotations: types.Annotations{
					apiv1_annotations.AnnotationDisableSeparateFeatureUpgrades: "true",
				},
			}, nil
		}
		getState := func() mctypes.State { return nil }
		setFeatureStatus := func(ctx context.Context, name types.FeatureName, featureStatus types.FeatureStatus) error {
			return nil
		}

		ctrl.Run(ctx, getClusterConfig, getState, notifyDNSChangedIP, setFeatureStatus)
		<-ctrl.ReadyCh()

		triggerDNSCh <- struct{}{}

		select {
		case <-ctrl.ReconciledDNSCh():
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for DNS reconcile")
		}

		g.Expect(notifyCount.Load()).To(Equal(int32(0)), "notifyDNSChangedIP must NOT be called when dnsIP is empty")
	})
}
