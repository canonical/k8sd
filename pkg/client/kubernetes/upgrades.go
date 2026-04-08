package kubernetes

import (
	"context"
	"fmt"
	"sort"

	upgradesv1alpha "github.com/canonical/k8s-snap-api/v2/api/v1alpha"
	"github.com/canonical/k8sd/pkg/log"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetUpgrade returns the latest upgrade CR that matches the given filter function.
// If multiple upgrades match, the most recently created one is returned and a warning is logged.
// If no upgrades match, nil is returned.
// If filterFunc is nil, all upgrades are considered matching.
func (c *Client) GetUpgrade(ctx context.Context, filterFunc func(upgradesv1alpha.Upgrade) bool) (*upgradesv1alpha.Upgrade, error) {
	log := log.FromContext(ctx).WithValues("upgrades", "GetUpgrade")

	// Default to a match-all predicate if no filter function is provided.
	if filterFunc == nil {
		filterFunc = func(upgradesv1alpha.Upgrade) bool { return true }
	}

	result := &upgradesv1alpha.UpgradeList{}
	if err := c.List(ctx, result); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get upgrades: %w", err)
	}

	var matches []upgradesv1alpha.Upgrade
	for _, upgrade := range result.Items {
		if filterFunc(upgrade) {
			matches = append(matches, upgrade)
		}
	}
	lenMatches := len(matches)
	if lenMatches == 0 {
		return nil, nil
	}
	if lenMatches > 1 {
		log.Info("Warning: Found multiple matching upgrades", "matches", lenMatches)
	}
	// Sort matches by creation timestamp
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreationTimestamp.Before(&matches[j].CreationTimestamp)
	})

	// Return the latest
	return &matches[lenMatches-1], nil
}

// GetInProgressUpgrade returns the upgrade CR that is currently in progress.
func (c *Client) GetInProgressUpgrade(ctx context.Context) (*upgradesv1alpha.Upgrade, error) {
	return c.GetUpgrade(ctx, func(u upgradesv1alpha.Upgrade) bool {
		return u.Status.Phase != upgradesv1alpha.UpgradePhaseFailed && u.Status.Phase != upgradesv1alpha.UpgradePhaseCompleted
	})
}

// PatchUpgradeStatus patches the status of an upgrade CR.
func (c *Client) PatchUpgradeStatus(ctx context.Context, u *upgradesv1alpha.Upgrade, status upgradesv1alpha.UpgradeStatus) error {
	p := ctrlclient.MergeFrom(u.DeepCopy())
	u.Status = status
	if err := c.Status().Patch(ctx, u, p); err != nil {
		return fmt.Errorf("failed to patch upgrade status: %w", err)
	}

	return nil
}
