package internal

import (
	"context"
	"errors"

	netnsutils "github.com/canonical/k8sd/pkg/utils/netns"
)

func RemoveNetworkNamespaces(ctx context.Context, netnsHelper netnsutils.NetworkNamespaceManager, owned map[string]bool) error {
	if len(owned) == 0 {
		return nil
	}
	var errs []error
	err := netnsHelper.ForEachNetworkNamespace(ctx, func(ctx context.Context, namespace string) error {
		if owned[namespace] {
			if err := netnsHelper.DeleteNetworkNamespace(ctx, namespace); err != nil {
				errs = append(errs, err)
			}
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
