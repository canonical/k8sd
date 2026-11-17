package k8sd

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
)

func TestRemoveNode(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.RemoveNodeRequest{Name: "node-to-remove"}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("POST"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.RemoveNodeRPC))
				r, ok := in.(apiv2.RemoveNodeRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.RemoveNode(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("failed to remove node")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.RemoveNode(ctx, apiv2.RemoveNodeRequest{Name: "node-to-remove"})
		g.Expect(err).To(HaveOccurred())
	})
}

func TestGetJoinToken(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.GetJoinTokenRequest{Worker: true}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("POST"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.GetJoinTokenRPC))
				r, ok := in.(apiv2.GetJoinTokenRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				if resp, ok := out.(*apiv2.GetJoinTokenResponse); ok {
					resp.EncodedToken = "secret-join-token"
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.GetJoinToken(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.EncodedToken).To(Equal("secret-join-token"))
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("cannot generate token")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.GetJoinToken(ctx, apiv2.GetJoinTokenRequest{Worker: true})
		g.Expect(err).To(HaveOccurred())
	})
}

func TestSetClusterAPIAuthToken(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.ClusterAPISetAuthTokenRequest{Token: "capi-token-val"}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("POST"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.ClusterAPISetAuthTokenRPC))
				r, ok := in.(apiv2.ClusterAPISetAuthTokenRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.SetClusterAPIAuthToken(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("capi failure")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.SetClusterAPIAuthToken(ctx, apiv2.ClusterAPISetAuthTokenRequest{Token: "bad"})
		g.Expect(err).To(HaveOccurred())
	})
}

func TestClusterConfig(t *testing.T) {
	t.Parallel()

	t.Run("SetClusterConfig_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cloudProvider := "external"
		req := apiv2.SetClusterConfigRequest{
			Config: apiv2.UserFacingClusterConfig{
				CloudProvider: &cloudProvider,
				Annotations:   map[string]string{"k8sd.io/test": "value"},
			},
		}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("PUT"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.SetClusterConfigRPC))
				r, ok := in.(apiv2.SetClusterConfigRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.SetClusterConfig(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("SetClusterConfig_Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("invalid config")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.SetClusterConfig(ctx, apiv2.SetClusterConfigRequest{})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("GetClusterConfig_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.GetClusterConfigRPC))
				g.Expect(in).To(BeNil())
				if resp, ok := out.(*apiv2.GetClusterConfigResponse); ok {
					cidr := "10.1.0.0/16"
					resp.PodCIDR = &cidr
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.GetClusterConfig(ctx)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.PodCIDR).ToNot(BeNil())
		g.Expect(*resp.PodCIDR).To(Equal("10.1.0.0/16"))
	})

	t.Run("GetClusterConfig_Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("cannot fetch config")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.GetClusterConfig(ctx)
		g.Expect(err).To(HaveOccurred())
	})
}

func TestManageCertificates(t *testing.T) {
	t.Parallel()

	t.Run("RefreshCertificatesPlan_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.RefreshCertificatesPlanRequest{Certificates: []string{"apiserver"}}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("POST"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.RefreshCertificatesPlanRPC))
				r, ok := in.(apiv2.RefreshCertificatesPlanRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				if resp, ok := out.(*apiv2.RefreshCertificatesPlanResponse); ok {
					resp.Seed = 42
					resp.CertificateSigningRequests = []string{"csr-1"}
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.RefreshCertificatesPlan(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.Seed).To(Equal(42))
		g.Expect(resp.CertificateSigningRequests).To(ConsistOf("csr-1"))
	})

	t.Run("RefreshCertificatesPlan_Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("plan error")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.RefreshCertificatesPlan(ctx, apiv2.RefreshCertificatesPlanRequest{})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("RefreshCertificatesRun_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.RefreshCertificatesRunRequest{
			Certificates:      []string{"apiserver"},
			Seed:              42,
			ExpirationSeconds: 3600,
			ExtraSANs:         []string{"extra.local"},
		}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("POST"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.RefreshCertificatesRunRPC))
				r, ok := in.(apiv2.RefreshCertificatesRunRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				if resp, ok := out.(*apiv2.RefreshCertificatesRunResponse); ok {
					resp.ExpirationSeconds = 3600
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.RefreshCertificatesRun(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.ExpirationSeconds).To(Equal(3600))
	})

	t.Run("RefreshCertificatesRun_Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("run error")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.RefreshCertificatesRun(ctx, apiv2.RefreshCertificatesRunRequest{})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("RefreshCertificatesUpdate_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		crt := "cert-pem"
		req := apiv2.RefreshCertificatesUpdateRequest{AdminClientCert: &crt}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("POST"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.RefreshCertificatesUpdateRPC))
				r, ok := in.(apiv2.RefreshCertificatesUpdateRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.RefreshCertificatesUpdate(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("RefreshCertificatesUpdate_Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("update error")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.RefreshCertificatesUpdate(ctx, apiv2.RefreshCertificatesUpdateRequest{})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("CertificatesStatus_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.CertificatesStatusRequest{}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.CertificatesStatusRPC))
				r, ok := in.(apiv2.CertificatesStatusRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				if resp, ok := out.(*apiv2.CertificatesStatusResponse); ok {
					resp.Certificates = []apiv2.CertificateStatus{{Name: "apiserver"}}
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.CertificatesStatus(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.Certificates).To(HaveLen(1))
		g.Expect(resp.Certificates[0].Name).To(Equal("apiserver"))
	})

	t.Run("CertificatesStatus_Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("status error")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.CertificatesStatus(ctx, apiv2.CertificatesStatusRequest{})
		g.Expect(err).To(HaveOccurred())
	})
}

func TestKubeConfig(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.KubeConfigRequest{Server: "https://k8s.example.com:6443"}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				g.Expect(method).To(Equal("GET"))
				g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
				g.Expect(path.Path).To(Equal("/" + apiv2.KubeConfigRPC))
				r, ok := in.(apiv2.KubeConfigRequest)
				g.Expect(ok).To(BeTrue())
				g.Expect(r).To(Equal(req))
				if resp, ok := out.(*apiv2.KubeConfigResponse); ok {
					resp.KubeConfig = "apiVersion: v1\nkind: Config"
				}
				return nil
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.KubeConfig(ctx, req)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(resp.KubeConfig).To(Equal("apiVersion: v1\nkind: Config"))
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := apiv2.KubeConfigRequest{Server: "https://k8s.example.com:6443"}
		fake := &fakeMicroclusterClient{
			queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
				return errors.New("cannot generate kubeconfig")
			},
		}
		client := &k8sd{client: fake}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.KubeConfig(ctx, req)
		g.Expect(err).To(HaveOccurred())
	})
}
