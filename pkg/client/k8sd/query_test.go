package k8sd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
)

func TestIsTemporary(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "DatabaseStarting",
			err:      errors.New(string(mctypes.DatabaseStarting)),
			expected: true,
		},
		{
			name:     "DatabaseStartingEmbedded",
			err:      fmt.Errorf("wrapped: %s in cluster", mctypes.DatabaseStarting),
			expected: true,
		},
		{
			name:     "DatabaseNotYetReady",
			err:      errors.New("Database is not yet ready"),
			expected: true,
		},
		{
			name:     "DatabaseNotYetReadyEmbedded",
			err:      errors.New("failed: Database is not yet ready to accept connections"),
			expected: true,
		},
		{
			name:     "DatabaseNotYetInitialized",
			err:      errors.New("Database is not yet initialized"),
			expected: false,
		},
		{
			name:     "OtherError",
			err:      errors.New("connection refused"),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isTemporary(tc.err)).To(Equal(tc.expected))
		})
	}
}

func TestBootstrapPending(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "DatabaseNotYetInitialized",
			err:      errors.New("Database is not yet initialized"),
			expected: true,
		},
		{
			name:     "DatabaseNotYetInitializedWrapped",
			err:      fmt.Errorf("error connecting to daemon: Database is not yet initialized"),
			expected: true,
		},
		{
			name:     "DatabaseStarting",
			err:      errors.New(string(mctypes.DatabaseStarting)),
			expected: false,
		},
		{
			name:     "OtherError",
			err:      errors.New("node not found"),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(bootstrapPending(tc.err)).To(Equal(tc.expected))
		})
	}
}

func TestQuery_NilOut(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	client := &k8sd{
		client: &fakeMicroclusterClient{},
	}

	_, err := query[string](context.Background(), client, "GET", "test-path", nil, nil)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("out must be a non-nil pointer"))
}

func TestQuery_Success(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	inPayload := map[string]string{"foo": "bar"}
	fake := &fakeMicroclusterClient{
		queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
			g.Expect(method).To(Equal("GET"))
			g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
			g.Expect(path.Path).To(Equal("/test/path"))
			g.Expect(in).To(Equal(inPayload))
			if resp, ok := out.(*apiv2.NodeStatusResponse); ok {
				resp.NodeStatus.Name = "node-1"
			}
			return nil
		},
	}
	client := &k8sd{client: fake}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out apiv2.NodeStatusResponse
	result, err := query(ctx, client, "GET", "test/path", inPayload, &out)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(result.NodeStatus.Name).To(Equal("node-1"))
	g.Expect(out.NodeStatus.Name).To(Equal("node-1"))
}

func TestQuery_RetryOnTemporary(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	var attempts int32
	fake := &fakeMicroclusterClient{
		queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
			g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
			g.Expect(path.Path).To(Equal("/retry/path"))
			n := atomic.AddInt32(&attempts, 1)
			if n == 1 {
				return errors.New("Database is not yet ready")
			}
			if resp, ok := out.(*apiv2.NodeStatusResponse); ok {
				resp.NodeStatus.Name = "node-after-retry"
			}
			return nil
		},
	}
	client := &k8sd{client: fake}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out apiv2.NodeStatusResponse
	result, err := query(ctx, client, "GET", "retry/path", nil, &out)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(atomic.LoadInt32(&attempts)).To(Equal(int32(2)))
	g.Expect(result.NodeStatus.Name).To(Equal("node-after-retry"))
}

func TestQuery_FastFailOnBootstrapPending(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	var attempts int32
	fake := &fakeMicroclusterClient{
		queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
			g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
			g.Expect(path.Path).To(Equal("/uninit/path"))
			atomic.AddInt32(&attempts, 1)
			return errors.New("Database is not yet initialized")
		},
	}
	client := &k8sd{client: fake}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out apiv2.NodeStatusResponse
	_, err := query(ctx, client, "GET", "uninit/path", nil, &out)
	g.Expect(err).To(HaveOccurred())
	g.Expect(atomic.LoadInt32(&attempts)).To(Equal(int32(1)))
	g.Expect(err.Error()).To(ContainSubstring("the cluster hasn't been initialized yet"))
}

func TestQuery_PermanentError(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	var attempts int32
	fake := &fakeMicroclusterClient{
		queryFunc: func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
			g.Expect(prefix).To(Equal(mctypes.EndpointPrefix(apiv2.K8sdAPIVersion)))
			g.Expect(path.Path).To(Equal("/error/path"))
			atomic.AddInt32(&attempts, 1)
			return errors.New("unauthorized request")
		},
	}
	client := &k8sd{client: fake}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out apiv2.NodeStatusResponse
	_, err := query(ctx, client, "POST", "error/path", nil, &out)
	g.Expect(err).To(HaveOccurred())
	g.Expect(atomic.LoadInt32(&attempts)).To(Equal(int32(1)))
	g.Expect(err.Error()).To(ContainSubstring("failed to POST /error/path"))
}

func TestQuery_ContextCanceled(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	client := &k8sd{client: &fakeMicroclusterClient{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	var out apiv2.NodeStatusResponse
	_, err := query(ctx, client, "GET", "canceled/path", nil, &out)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("context canceled"))
}
