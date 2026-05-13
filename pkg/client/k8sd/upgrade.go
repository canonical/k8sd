package k8sd

import (
	"context"
	"fmt"
)

type UpgradeCheckRequest struct {
	FromVersion string `json:"from-version" yaml:"from-version"`
	ToVersion   string `json:"to-version" yaml:"to-version"`
}

type UpgradeWarning struct {
	Severity  string `json:"severity" yaml:"severity"`
	Component string `json:"component" yaml:"component"`
	Message   string `json:"message" yaml:"message"`
}

type UpgradeComponentResult struct {
	Name         string           `json:"name" yaml:"name"`
	FromVersion  string           `json:"from_version" yaml:"from_version"`
	ToVersion    string           `json:"to_version" yaml:"to_version"`
	RepoURL      string           `json:"repo_url" yaml:"repo_url"`
	Verdict      string           `json:"verdict" yaml:"verdict"`
	Warnings     []UpgradeWarning `json:"warnings" yaml:"warnings"`
	Remediations []string         `json:"remediations" yaml:"remediations"`
}

type UpgradeCheckResponse struct {
	FromVersion string                   `json:"from_version" yaml:"from_version"`
	ToVersion   string                   `json:"to_version" yaml:"to_version"`
	Verdict     string                   `json:"verdict" yaml:"verdict"`
	Components  []UpgradeComponentResult `json:"components" yaml:"components"`
	Summary     string                   `json:"summary" yaml:"summary"`
}

func (c *k8sd) UpgradeCheck(ctx context.Context, request UpgradeCheckRequest) (UpgradeCheckResponse, error) {
	return UpgradeCheckResponse{}, fmt.Errorf("not yet implemented")
}