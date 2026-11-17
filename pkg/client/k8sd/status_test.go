package k8sd

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/lxd/shared/api"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
)

func TestNodeStatus(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.NodeStatusRPC))
				g.Expect(in).To(BeNil())
				if resp, ok := out.(*apiv2.NodeStatusResponse); ok {
					resp.NodeStatus.Name = "k8s-node-1"
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, ok, err := client.NodeStatus(ctx)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(ok).To(BeTrue())
		g.Expect(resp.NodeStatus.Name).To(Equal("k8s-node-1"))
	})

	t.Run("StatusError503ReturnsEmptyFalseNil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.NodeStatusRPC))
				return api.NewStatusError(http.StatusServiceUnavailable, "service unavailable")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, ok, err := client.NodeStatus(ctx)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(ok).To(BeFalse())
		g.Expect(resp).To(Equal(apiv2.NodeStatusResponse{}))
	})

	t.Run("StatusError500ReturnsError", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.NodeStatusRPC))
				return api.NewStatusError(http.StatusInternalServerError, "internal server error")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, ok, err := client.NodeStatus(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(ok).To(BeFalse())
	})

	t.Run("GenericErrorReturnsError", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.NodeStatusRPC))
				return errors.New("connection reset by peer")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, ok, err := client.NodeStatus(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(ok).To(BeFalse())
	})
}

func TestClusterStatus(t *testing.T) {
	t.Parallel()

	t.Run("WaitReadyFalse_ReturnsWhenNotReady", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.ClusterStatusRPC))
				g.Expect(in).To(BeNil())
				if resp, ok := out.(*apiv2.ClusterStatusResponse); ok {
					resp.ClusterStatus.Ready = false
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.ClusterStatus(ctx, false)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.ClusterStatus.Ready).To(BeFalse())
	})

	t.Run("WaitReadyTrue_PollsUntilReady", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var attempts int32
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.ClusterStatusRPC))
				g.Expect(in).To(BeNil())

				n := atomic.AddInt32(&attempts, 1)
				if resp, ok := out.(*apiv2.ClusterStatusResponse); ok {
					resp.ClusterStatus.Ready = (n >= 2)
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp, err := client.ClusterStatus(ctx, true)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.ClusterStatus.Ready).To(BeTrue())
		g.Expect(atomic.LoadInt32(&attempts)).To(Equal(int32(2)))
	})

	t.Run("QueryFails_ReturnsError", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.ClusterStatusRPC))
				return errors.New("dqlite error")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.ClusterStatus(ctx, false)
		g.Expect(err).To(HaveOccurred())
	})
}
