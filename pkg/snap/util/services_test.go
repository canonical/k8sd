package snaputil

import (
	"context"
	"fmt"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestStartWorkerServices(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{},
	}
	g := NewWithT(t)

	mock.StartServicesErr = fmt.Errorf("service start failed")

	t.Run("AllServicesStartSuccess", func(t *testing.T) {
		mock.StartServicesCalledWith = nil
		mock.StartServicesErr = nil
		g.Expect(StartWorkerServices(context.Background(), mock, types.ClusterConfig{})).To(Succeed())
		g.Expect(mock.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StartServicesCalledWith[0]).To(ConsistOf(workerK8sServices))
	})

	t.Run("KubeProxyDisabled", func(t *testing.T) {
		mock.StartServicesCalledWith = nil
		mock.StartServicesErr = nil
		cfg := types.ClusterConfig{}
		cfg.Network.KubeProxyEnabled = utils.Pointer(false)
		g.Expect(StartWorkerServices(context.Background(), mock, cfg)).To(Succeed())
		g.Expect(mock.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StartServicesCalledWith[0]).NotTo(ContainElement("kube-proxy"))
		g.Expect(mock.StartServicesCalledWith[0]).To(ContainElements("containerd", "k8s-apiserver-proxy", "kubelet"))
	})

	t.Run("ServiceStartFailure", func(t *testing.T) {
		mock.StartServicesErr = fmt.Errorf("service start failed")
		g.Expect(StartWorkerServices(context.Background(), mock, types.ClusterConfig{})).NotTo(Succeed())
	})
}

func TestStartControlPlaneServices(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{},
	}
	g := NewWithT(t)

	mock.StartServicesErr = fmt.Errorf("service start failed")

	t.Run("AllServicesStartSuccess", func(t *testing.T) {
		mock.StartServicesCalledWith = nil
		mock.StartServicesErr = nil
		g.Expect(StartControlPlaneServices(context.Background(), mock, types.ClusterConfig{})).To(Succeed())
		g.Expect(mock.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StartServicesCalledWith[0]).To(ConsistOf(controlPlaneK8sServices))
	})

	t.Run("KubeProxyDisabled", func(t *testing.T) {
		mock.StartServicesCalledWith = nil
		mock.StartServicesErr = nil
		cfg := types.ClusterConfig{}
		cfg.Network.KubeProxyEnabled = utils.Pointer(false)
		g.Expect(StartControlPlaneServices(context.Background(), mock, cfg)).To(Succeed())
		g.Expect(mock.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StartServicesCalledWith[0]).NotTo(ContainElement("kube-proxy"))
		g.Expect(mock.StartServicesCalledWith[0]).To(ContainElements("containerd", "kube-controller-manager", "kube-scheduler", "kubelet", "kube-apiserver"))
	})

	t.Run("ServiceStartFailure", func(t *testing.T) {
		mock.StartServicesErr = fmt.Errorf("service start failed")
		g.Expect(StartControlPlaneServices(context.Background(), mock, types.ClusterConfig{})).NotTo(Succeed())
	})
}

func TestStartEtcdServices(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{},
	}
	g := NewWithT(t)

	mock.StartServicesErr = fmt.Errorf("service start failed")

	t.Run("ServiceStartSuccess", func(t *testing.T) {
		mock.StartServicesErr = nil
		g.Expect(StartEtcdServices(context.Background(), mock)).To(Succeed())
		g.Expect(mock.StartServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StartServicesCalledWith[0]).To(ConsistOf("etcd"))
	})

	t.Run("ServiceStartFailure", func(t *testing.T) {
		mock.StartServicesErr = fmt.Errorf("service start failed")
		g.Expect(StartEtcdServices(context.Background(), mock)).NotTo(Succeed())
	})
}

func TestStopEtcdServices(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{},
	}
	g := NewWithT(t)

	mock.StopServicesErr = fmt.Errorf("service stop failed")

	t.Run("ServiceStopSuccess", func(t *testing.T) {
		mock.StopServicesErr = nil
		g.Expect(StopEtcdServices(context.Background(), mock)).To(Succeed())
		g.Expect(mock.StopServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StopServicesCalledWith[0]).To(ConsistOf("etcd"))
	})

	t.Run("ServiceStopFailure", func(t *testing.T) {
		mock.StopServicesErr = fmt.Errorf("service stop failed")
		g.Expect(StopEtcdServices(context.Background(), mock)).NotTo(Succeed())
	})
}

func TestStopK8sServices(t *testing.T) {
	mock := &mock.Snap{
		Mock: mock.Mock{},
	}
	g := NewWithT(t)

	mock.StopServicesErr = fmt.Errorf("service stop failed")

	t.Run("ServiceStopSuccess", func(t *testing.T) {
		mock.StopServicesErr = nil
		g.Expect(StopK8sServices(context.Background(), mock)).To(Succeed())
		g.Expect(mock.StopServicesCalledWith).To(HaveLen(1))
		g.Expect(mock.StopServicesCalledWith[0]).To(ConsistOf(k8sServices))
	})

	t.Run("ServiceStopFailure", func(t *testing.T) {
		mock.StopServicesErr = fmt.Errorf("service stop failed")
		g.Expect(StopEtcdServices(context.Background(), mock)).NotTo(Succeed())
	})
}
