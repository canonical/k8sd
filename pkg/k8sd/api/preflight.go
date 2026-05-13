package api

import (
	"fmt"
	"net/http"

	"github.com/canonical/k8sd/pkg/client/k8sd"
	"github.com/canonical/k8sd/pkg/k8sd/preflight"
	"github.com/canonical/k8sd/pkg/utils"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) postUpgradeCheck(s mctypes.State, r *http.Request) mctypes.Response {
	var req k8sd.UpgradeCheckRequest
	if err := utils.NewStrictJSONDecoder(r.Body).Decode(&req); err != nil {
		return mctypes.BadRequest(fmt.Errorf("failed to parse request: %w", err))
	}

	if req.ToVersion == "" {
		return mctypes.BadRequest(fmt.Errorf("to_version is required"))
	}

	checker := preflight.NewChecker(
		&preflight.MockAnalyzer{},
		preflight.SnapDownloaderFromSnap(),
	)

	result, err := checker.CheckTargetChannel(r.Context(), req.FromVersion, req.ToVersion)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("upgrade check failed: %w", err))
	}

	return mctypes.SyncResponse(true, convertUpgradeResult(result))
}

func convertUpgradeResult(r *preflight.PreflightResult) k8sd.UpgradeCheckResponse {
	components := make([]k8sd.UpgradeComponentResult, len(r.Components))
	for i, c := range r.Components {
		warnings := make([]k8sd.UpgradeWarning, len(c.Warnings))
		for j, w := range c.Warnings {
			warnings[j] = k8sd.UpgradeWarning{
				Severity:  w.Severity,
				Component: w.Component,
				Message:   w.Message,
			}
		}
		remediations := make([]string, len(c.Remediations))
		for j, rem := range c.Remediations {
			remediations[j] = rem.Description
		}
		components[i] = k8sd.UpgradeComponentResult{
			Name:         c.Delta.Name,
			FromVersion:  c.Delta.FromVersion,
			ToVersion:    c.Delta.ToVersion,
			RepoURL:      c.Delta.RepoURL,
			Verdict:      string(c.Verdict),
			Warnings:     warnings,
			Remediations: remediations,
		}
	}

	return k8sd.UpgradeCheckResponse{
		FromVersion: r.FromChannel,
		ToVersion:   r.ToChannel,
		Verdict:     string(r.Verdict),
		Components:  components,
		Summary:     r.Summary,
	}
}
