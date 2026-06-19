package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	releasepkg "helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// client implements Client using Helm.
type client struct {
	restClientGetter func(string) genericclioptions.RESTClientGetter
	manifestsBaseDir string
	// timeout for helm operations.
	timeout time.Duration
	// maxHistory specifies the maximum number of historical releases that will
	// be retained, including the most recent release. Values of 0 or less are
	// ignored (meaning no limits are imposed).
	maxHistory int

	// lastPresentValues stores the sanitizedValues from the most recent successful
	// StatePresent Apply call, keyed by chart name.
	//
	// For StatePresent charts that are also modified by StateUpgradeOnly callers
	// (e.g. ApplyGateway/ApplyIngress adding keys to ChartCilium), comparing
	// sanitizedValues directly against the Helm release's oldConfig would produce
	// false positives: the extra keys in oldConfig would look like a change.
	// By remembering what WE last applied, we can tell whether OUR values changed
	// without being confused by keys added by other callers.
	//
	// The cache is intentionally not persisted across k8sd restarts. On the first
	// StatePresent call after a restart the cache is empty and we always run an
	// upgrade, which ensures any pending changes (e.g. a ConfigMap override deleted
	// while k8sd was down) are applied correctly.
	lastPresentValues   map[string]map[string]any
	lastPresentValuesMu sync.Mutex
}

// ensure *client implements Client.
var _ Client = &client{}

// NewClient creates a new client.
func NewClient(manifestsBaseDir string,
	restClientGetter func(string) genericclioptions.RESTClientGetter,
	applyTimeout time.Duration,
	maxHistory int,
) *client {
	return &client{
		restClientGetter: restClientGetter,
		manifestsBaseDir: manifestsBaseDir,
		timeout:          applyTimeout,
		maxHistory:       maxHistory,
	}
}

func (h *client) newActionConfiguration(ctx context.Context, namespace string) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)

	log := log.FromContext(ctx).WithName("helm")
	if err := actionConfig.Init(h.restClientGetter(namespace), namespace, "", func(format string, v ...interface{}) {
		log.Info(fmt.Sprintf(format, v...))
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize: %w", err)
	}
	return actionConfig, nil
}

// Apply implements the Client interface.
func (h *client) Apply(ctx context.Context, c InstallableChart, desired State, values map[string]any) (bool, error) {
	log := log.FromContext(ctx).WithName("helm").WithValues("chart", c.Name, "desired", desired)

	cfg, err := h.newActionConfiguration(ctx, c.Namespace)
	if err != nil {
		return false, fmt.Errorf("failed to create action configuration: %w", err)
	}

	isInstalled := true
	var oldConfig map[string]any

	// get the latest Helm release with the specified name
	get := action.NewGet(cfg)
	release, err := get.Run(c.Name)
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return false, fmt.Errorf("failed to get status of release %s: %w", c.Name, err)
		}
		isInstalled = false
	} else {
		// keep the existing release configuration, to check if any changes were made.
		oldConfig = release.Config
	}

	// If the release is installed, we need to check if it is in a pending state.
	// If it is, we need to change its status, so that it can be reinstalled or upgraded.
	if isInstalled && release.Info.Status.IsPending() {
		// NOTE(Hue): We're updating the status to "failed", so that future reconciliations
		// (helm operations) can proceed without being blocked by the pending state.
		// Another proposed approach would be to delete the pending revision's secret,
		// but that would introduce various issues and edge cases.
		s := releasepkg.StatusFailed
		log.Info("release is in a pending state, changing status", "status", release.Info.Status, "chart", c.Name, "target_status", s)

		release.Info.Status = s
		if err := cfg.Releases.Update(release); err != nil {
			return false, fmt.Errorf("failed to update release %s status: %w", c.Name, err)
		}
	}

	sanitizedValues, err := sanitizeHelmValues(values)
	if err != nil {
		return false, fmt.Errorf("failed to convert values: %w", err)
	}

	switch {
	case !isInstalled && desired == StateDeleted:
		// no-op
		return false, nil
	case !isInstalled && desired == StateUpgradeOnly:
		// there is no release installed, this is an error
		return false, fmt.Errorf("cannot upgrade %s as it is not installed", c.Name)
	case !isInstalled && desired == StatePresent:
		// there is no release installed, so we must run an install action
		install := action.NewInstall(cfg)
		install.Timeout = h.timeout
		install.ReleaseName = c.Name
		install.Namespace = c.Namespace
		install.CreateNamespace = true

		chart, err := loader.Load(filepath.Join(h.manifestsBaseDir, c.ManifestPath))
		if err != nil {
			return false, fmt.Errorf("failed to load manifest for %s: %w", c.Name, err)
		}

		if _, err := install.RunWithContext(ctx, chart, sanitizedValues); err != nil {
			return false, fmt.Errorf("failed to install %s: %w", c.Name, err)
		}
		h.setLastPresentValues(c.Name, sanitizedValues)
		return true, nil
	case isInstalled && desired != StateDeleted:
		chart, err := loader.Load(filepath.Join(h.manifestsBaseDir, c.ManifestPath))
		if err != nil {
			return false, fmt.Errorf("failed to load manifest for %s: %w", c.Name, err)
		}

		// NOTE(Angelos): oldConfig and values are the previous and current values. they are compared by checking their respective JSON, as that is good enough for our needs of comparing unstructured map[string]any data.
		// NOTE(Hue) (KU-3592): We are ignoring the values that are overwritten by the user.
		// The user can change some values in the chart, but we will revert them back upon an upgrade.
		//
		// For StatePresent calls (full state description), we compare sanitizedValues against
		// what WE last applied (stored in lastPresentValues), not against oldConfig. This avoids
		// false-positive upgrades when other features add keys to the same chart via
		// StateUpgradeOnly (e.g. ApplyGateway adds "gatewayAPI" to ChartCilium), while still
		// correctly detecting removals (e.g. a ConfigMap override key being deleted).
		//
		// If no cached value exists yet (first reconciliation after k8sd starts), we always run
		// the upgrade. This ensures any changes made while k8sd was down (e.g. a ConfigMap
		// override deleted between restarts) are applied on the first reconciliation.
		//
		// For StateUpgradeOnly calls (partial update, e.g. ApplyGateway/ApplyIngress sharing
		// ChartCilium), we merge sanitizedValues into oldConfig before comparing so that keys
		// owned by other features are not treated as a change.
		var sameValues bool
		if desired == StatePresent {
			if prev, ok := h.getLastPresentValues(c.Name); ok {
				sameValues = jsonEqual(prev, sanitizedValues)
			}
			// If !ok (no cache entry yet), sameValues stays false: always upgrade on first call.
		} else {
			// CoalesceTables mutates its first argument, so clone sanitizedValues first.
			clonedValues, _ := sanitizeHelmValues(sanitizedValues)
			mergedValues := chartutil.CoalesceTables(clonedValues, oldConfig)
			sameValues = jsonEqual(oldConfig, mergedValues)
		}
		// NOTE(Hue): For the charts that we manage (e.g. ck-loadbalancer), we need to make
		// sure we bump the version manually. Otherwise, they'll not be applied unless
		// we're lucky and providing different extra values.
		sameVersions := release.Chart.Metadata.Version == chart.Metadata.Version
		switch {
		case sameValues && sameVersions:
			if release.Info.Status == releasepkg.StatusDeployed || release.Info.Status == releasepkg.StatusSuperseded {
				log.Info("no changes detected, skipping upgrade", "status", release.Info.Status)
				// Keep cache current (it already equals sanitizedValues, but be explicit).
				if desired == StatePresent {
					h.setLastPresentValues(c.Name, sanitizedValues)
				}
				return false, nil
			}
			log.Info(fmt.Sprintf("no changes detected, but release status is %q, proceeding with upgrade", release.Info.Status))
		case sameValues && !sameVersions:
			log.Info("chart version changed, upgrading", "oldVersion", release.Chart.Metadata.Version, "newVersion", chart.Metadata.Version)
		case sameVersions && !sameValues:
			log.Info("values changed, upgrading")
		default:
			log.Info("both chart version and values changed, upgrading", "oldVersion", release.Chart.Metadata.Version, "newVersion", chart.Metadata.Version)
		}

		// there is already a release installed, so we must run an upgrade action
		upgrade := action.NewUpgrade(cfg)
		upgrade.Namespace = c.Namespace
		// For StatePresent (full state description), ResetValues ensures k8sd is the sole
		// source of truth for Helm values: keys removed from a ConfigMap override are properly
		// reverted and any values set externally are not preserved.
		// For StateUpgradeOnly (partial update), ResetThenReuseValues preserves values set by
		// other features that share the same chart (e.g. Gateway/Ingress sharing ChartCilium).
		upgrade.ResetValues = desired == StatePresent
		upgrade.ResetThenReuseValues = desired != StatePresent
		upgrade.Timeout = h.timeout
		// NOTE(Hue): We need to set the upgrade.MaxHistory here since it overwrites the
		// cfg.Releases.MaxHistory value.
		upgrade.MaxHistory = h.maxHistory

		if _, err := upgrade.RunWithContext(ctx, c.Name, chart, sanitizedValues); err != nil {
			return false, fmt.Errorf("failed to upgrade %s: %w", c.Name, err)
		}
		if desired == StatePresent {
			h.setLastPresentValues(c.Name, sanitizedValues)
		}

		return true, nil
	case isInstalled && desired == StateDeleted:
		// run an uninstall action
		uninstall := action.NewUninstall(cfg)
		uninstall.Timeout = h.timeout
		if _, err := uninstall.Run(c.Name); err != nil {
			return false, fmt.Errorf("failed to uninstall %s: %w", c.Name, err)
		}

		return true, nil
	default:
		// this never happens
		return false, nil
	}
}

func jsonEqual(v1 any, v2 any) bool {
	b1, err1 := json.Marshal(v1)
	b2, err2 := json.Marshal(v2)
	return err1 == nil && err2 == nil && bytes.Equal(b1, b2)
}

// getLastPresentValues returns the sanitizedValues from the most recent successful
// StatePresent Apply call for the named chart, and whether an entry exists.
func (h *client) getLastPresentValues(chartName string) (map[string]any, bool) {
	h.lastPresentValuesMu.Lock()
	defer h.lastPresentValuesMu.Unlock()
	v, ok := h.lastPresentValues[chartName]
	return v, ok
}

// setLastPresentValues records sanitizedValues as the last applied values for
// the named chart, creating the map on first use.
func (h *client) setLastPresentValues(chartName string, values map[string]any) {
	h.lastPresentValuesMu.Lock()
	defer h.lastPresentValuesMu.Unlock()
	if h.lastPresentValues == nil {
		h.lastPresentValues = make(map[string]map[string]any)
	}
	h.lastPresentValues[chartName] = values
}

// sanitizeHelmValues converts a map[string]any to map[string]any with properly
// typed nested structures for compatibility with Helm's JSON schema validator.
//
// NOTE: The Helm library (specifically santhosh-tekuri/jsonschema v6) expects
// arrays as []any and maps as map[string]any. Go's typed slices like []string
// or []map[string]any are not recognized as valid JSON types by the
// validator, causing "invalid jsonType" errors.
// https://github.com/santhosh-tekuri/jsonschema/issues/238
func sanitizeHelmValues(values map[string]any) (map[string]any, error) {
	jsonBytes, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Helm values to JSON for schema validation: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to convert JSON back to Helm-compatible value types: %w", err)
	}

	return result, nil
}
