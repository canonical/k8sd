package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"helm.sh/helm/v3/pkg/action"
	chartpkg "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	releasepkg "helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// upgradeRunner runs a Helm upgrade action.
type upgradeRunner func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error)

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
	// upgradeRunner executes an upgrade action. Defaults to u.RunWithContext.
	upgradeRunner upgradeRunner
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
		return true, nil
	case isInstalled && desired != StateDeleted:
		chart, err := loader.Load(filepath.Join(h.manifestsBaseDir, c.ManifestPath))
		if err != nil {
			return false, fmt.Errorf("failed to load manifest for %s: %w", c.Name, err)
		}

		// NOTE(Angelos): oldConfig and values are the previous and current values. they are compared by checking their respective JSON, as that is good enough for our needs of comparing unstructured map[string]any data.
		// NOTE(Hue) (KU-3592): We are ignoring the values that are overwritten by the user.
		// The user can change some values in the chart, but we will revert them back upon an upgrade.
		// NOTE(Hue): We clone the values map to avoid modifying the original user provided values.
		clonedValues, err := cloneMap(sanitizedValues)
		if err != nil {
			return false, fmt.Errorf("failed to clone values for %s: %w", c.Name, err)
		}
		mergedValues := chartutil.CoalesceTables(clonedValues, oldConfig)
		sameValues := jsonEqual(oldConfig, mergedValues)
		// NOTE(Hue): For the charts that we manage (e.g. ck-loadbalancer), we need to make
		// sure we bump the version manually. Otherwise, they'll not be applied unless
		// we're lucky and providing different extra values.
		sameVersions := release.Chart.Metadata.Version == chart.Metadata.Version
		switch {
		case sameValues && sameVersions:
			if release.Info.Status == releasepkg.StatusDeployed || release.Info.Status == releasepkg.StatusSuperseded {
				log.Info("no changes detected, skipping upgrade", "status", release.Info.Status)
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

		// there is already a release installed, so we must run an upgrade action.
		if err := h.upgradeChart(ctx, cfg, c.Name, c.Namespace, chart, sanitizedValues); err != nil {
			return false, err
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

// cloneMap creates a deep copy of a map[string]any by marshaling and unmarshaling it.
func cloneMap(m map[string]any) (map[string]any, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal map: %w", err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(b, &cloned); err != nil {
		return nil, fmt.Errorf("failed to unmarshal map: %w", err)
	}
	return cloned, nil
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

// upgradeChart executes a Helm upgrade, retrying once with Force if the initial
// attempt fails due to a rejected patch.
func (h *client) upgradeChart(ctx context.Context, cfg *action.Configuration, name, namespace string, ch *chartpkg.Chart, vals map[string]any) error {
	log := log.FromContext(ctx).WithName("helm").WithValues("chart", name)

	newUpgrade := func() *action.Upgrade {
		u := action.NewUpgrade(cfg)
		u.Namespace = namespace
		u.ResetThenReuseValues = true
		u.Timeout = h.timeout
		u.MaxHistory = h.maxHistory
		return u
	}

	runner := h.upgradeRunner
	if runner == nil {
		runner = func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error) {
			return u.RunWithContext(ctx, name, ch, vals)
		}
	}

	upgrade := newUpgrade()
	if _, err := runner(ctx, upgrade, name, ch, vals); err != nil {
		if !isInvalidPatchError(err) {
			return fmt.Errorf("failed to upgrade %s: %w", name, err)
		}

		// Force full-replaces every resource in the release, not just the
		// one that failed to patch; any out-of-band field on any of them
		// is lost.
		log.Info("upgrade failed with a rejected patch, retrying with force", "error", err)
		forceUpgrade := newUpgrade()
		forceUpgrade.Force = true
		if _, forceErr := runner(ctx, forceUpgrade, name, ch, vals); forceErr != nil {
			return fmt.Errorf("failed to upgrade %s (force retry also failed: %w): %w", name, forceErr, err)
		}
	}

	return nil
}

// isInvalidPatchError returns true if err indicates the Kubernetes API server
// rejected a 3-way merge patch during an upgrade.
//
// Helm's pkg/kube client collects patch errors as stringified error messages
// and returns errors.New(strings.Join(updateErrors, " && ")), which strips the
// underlying *apierrors.StatusError type. We therefore require Helm's "cannot patch"
// operation context, combined with either apierrors.IsInvalid for structured errors
// or string matching for Helm's wrapped "is invalid" error text.
func isInvalidPatchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "cannot patch") {
		return false
	}
	return apierrors.IsInvalid(err) || strings.Contains(msg, "is invalid")
}
