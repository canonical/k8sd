package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/canonical/k8sd/pkg/log"
	"github.com/go-logr/logr"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
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
	newConfig  func(ctx context.Context, namespace string) (*action.Configuration, error)
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

func (h *client) actionConfiguration(ctx context.Context, namespace string) (*action.Configuration, error) {
	if h.newConfig != nil {
		return h.newConfig(ctx, namespace)
	}
	return h.newActionConfiguration(ctx, namespace)
}

func (h *client) newActionConfiguration(ctx context.Context, namespace string) (*action.Configuration, error) {
	log := log.FromContext(ctx).WithName("helm")
	actionConfig := action.NewConfiguration(action.ConfigurationSetLogger(logr.ToSlogHandler(log)))

	if err := actionConfig.Init(h.restClientGetter(namespace), namespace, ""); err != nil {
		return nil, fmt.Errorf("failed to initialize: %w", err)
	}
	return actionConfig, nil
}

func chartVersion(ch chart.Charter) string {
	switch c := ch.(type) {
	case *chartv2.Chart:
		if c != nil && c.Metadata != nil {
			return c.Metadata.Version
		}
	case chartv2.Chart:
		if c.Metadata != nil {
			return c.Metadata.Version
		}
	}
	if acc, err := chart.NewAccessor(ch); err == nil && acc != nil {
		if v, ok := acc.MetadataAsMap()["version"].(string); ok {
			return v
		}
	}
	return ""
}

// Apply implements the Client interface.
func (h *client) Apply(ctx context.Context, c InstallableChart, desired State, values map[string]any) (bool, error) {
	log := log.FromContext(ctx).WithName("helm").WithValues("chart", c.Name, "desired", desired)

	cfg, err := h.actionConfiguration(ctx, c.Namespace)
	if err != nil {
		return false, fmt.Errorf("failed to create action configuration: %w", err)
	}

	isInstalled := true
	var oldConfig map[string]any

	// get the latest Helm release with the specified name
	get := action.NewGet(cfg)
	rel, err := get.Run(c.Name)
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return false, fmt.Errorf("failed to get status of release %s: %w", c.Name, err)
		}
		isInstalled = false
	}

	var relObj *releasev1.Release
	if isInstalled {
		var ok bool
		relObj, ok = rel.(*releasev1.Release)
		if !ok {
			if r, okVal := rel.(releasev1.Release); okVal {
				relObj = &r
			} else {
				return false, fmt.Errorf("unsupported release type %T for %s", rel, c.Name)
			}
		}
		// keep the existing release configuration, to check if any changes were made.
		oldConfig = relObj.Config
	}

	// If the release is installed, we need to check if it is in a pending state.
	// If it is, we need to change its status, so that it can be reinstalled or upgraded.
	if isInstalled && relObj != nil && relObj.Info != nil && relObj.Info.Status.IsPending() {
		// NOTE(Hue): We're updating the status to "failed", so that future reconciliations
		// (helm operations) can proceed without being blocked by the pending state.
		// Another proposed approach would be to delete the pending revision's secret,
		// but that would introduce various issues and edge cases.
		s := releasecommon.StatusFailed
		log.Info("release is in a pending state, changing status", "status", relObj.Info.Status, "chart", c.Name, "target_status", s)

		relObj.Info.Status = s
		if err := cfg.Releases.Update(relObj); err != nil {
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
		install.WaitStrategy = kube.HookOnlyStrategy
		install.ServerSideApply = false
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

		targetValues, err := resolveUpgradeValues(desired, sanitizedValues, oldConfig, c.RetainKeys)
		if err != nil {
			return false, fmt.Errorf("failed to resolve values for %s: %w", c.Name, err)
		}
		sameValues := jsonEqual(normalizeValues(oldConfig), targetValues)
		// NOTE(Hue): For the charts that we manage (e.g. ck-loadbalancer), we need to make
		// sure we bump the version manually. Otherwise, they'll not be applied unless
		// we're lucky and providing different extra values.
		oldVersion := ""
		if relObj != nil {
			oldVersion = chartVersion(relObj.Chart)
		}
		newVersion := chartVersion(chart)
		sameVersions := oldVersion != "" && oldVersion == newVersion
		switch {
		case sameValues && sameVersions:
			if relObj != nil && relObj.Info != nil && (relObj.Info.Status == releasecommon.StatusDeployed || relObj.Info.Status == releasecommon.StatusSuperseded) {
				log.Info("no changes detected, skipping upgrade", "status", relObj.Info.Status)
				return false, nil
			}
			var status releasecommon.Status
			if relObj != nil && relObj.Info != nil {
				status = relObj.Info.Status
			}
			log.Info(fmt.Sprintf("no changes detected, but release status is %q, proceeding with upgrade", status))
		case sameValues && !sameVersions:
			log.Info("chart version changed, upgrading", "oldVersion", oldVersion, "newVersion", newVersion)
		case sameVersions && !sameValues:
			log.Info("values changed, upgrading")
		default:
			log.Info("both chart version and values changed, upgrading", "oldVersion", oldVersion, "newVersion", newVersion)
		}

		// there is already a release installed, so we must run an upgrade action
		upgrade := action.NewUpgrade(cfg)
		upgrade.WaitStrategy = kube.HookOnlyStrategy
		upgrade.ServerSideApply = "false"
		upgrade.Namespace = c.Namespace
		upgrade.ResetValues = true
		upgrade.Timeout = h.timeout
		// NOTE(Hue): We need to set the upgrade.MaxHistory here since it overwrites the
		// cfg.Releases.MaxHistory value.
		upgrade.MaxHistory = h.maxHistory

		if _, err := upgrade.RunWithContext(ctx, c.Name, chart, targetValues); err != nil {
			return false, fmt.Errorf("failed to upgrade %s: %w", c.Name, err)
		}

		return true, nil
	case isInstalled && desired == StateDeleted:
		// run an uninstall action
		uninstall := action.NewUninstall(cfg)
		uninstall.WaitStrategy = kube.HookOnlyStrategy
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

func normalizeValues(m map[string]any) map[string]any {
	if len(m) == 0 {
		return map[string]any{}
	}
	return m
}

func resolveUpgradeValues(desired State, values, oldConfig map[string]any, retainKeys []string) (map[string]any, error) {
	if desired == StateUpgradeOnly {
		target, err := cloneMap(normalizeValues(oldConfig))
		if err != nil {
			return nil, err
		}
		patch, err := cloneMap(normalizeValues(values))
		if err != nil {
			return nil, err
		}
		for k, v := range patch {
			if v == nil {
				delete(target, k)
			} else {
				target[k] = v
			}
		}
		return target, nil
	}

	target, err := cloneMap(normalizeValues(values))
	if err != nil {
		return nil, err
	}
	if len(retainKeys) > 0 && len(oldConfig) > 0 {
		prev, err := cloneMap(oldConfig)
		if err != nil {
			return nil, err
		}
		for _, k := range retainKeys {
			if _, exists := target[k]; !exists {
				if v, ok := prev[k]; ok {
					target[k] = v
				}
			}
		}
	}
	return target, nil
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
	if len(values) == 0 {
		return map[string]any{}, nil
	}
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
