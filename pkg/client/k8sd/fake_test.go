package k8sd

import (
	"context"
	"net/url"

	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

type fakeMicroclusterClient struct {
	mctypes.Client
	queryFunc func(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error
}

func (f *fakeMicroclusterClient) Query(ctx context.Context, method string, prefix mctypes.EndpointPrefix, path *url.URL, in any, out any) error {
	if f.queryFunc != nil {
		return f.queryFunc(ctx, method, prefix, path, in, out)
	}
	return nil
}
