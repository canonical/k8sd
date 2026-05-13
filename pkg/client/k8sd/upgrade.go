package k8sd

import (
	"context"
)

type UpgradeCheckRequest struct {
	FromChannel string `json:"from-channel" yaml:"from-channel"`
	ToChannel   string `json:"to-channel" yaml:"to-channel"`
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
	Verdict      string           `json:"verdict" yaml:"verdict"`
	Warnings     []UpgradeWarning `json:"warnings" yaml:"warnings"`
	Remediations []string         `json:"remediations" yaml:"remediations"`
}

type UpgradeCheckResponse struct {
	FromChannel string                   `json:"from_channel" yaml:"from_channel"`
	ToChannel   string                   `json:"to_channel" yaml:"to_channel"`
	Verdict     string                   `json:"verdict" yaml:"verdict"`
	Components  []UpgradeComponentResult `json:"components" yaml:"components"`
	Summary     string                   `json:"summary" yaml:"summary"`
}

func (c *k8sd) UpgradeCheck(ctx context.Context, request UpgradeCheckRequest) (UpgradeCheckResponse, error) {
	return query(ctx, c, "POST", "k8sd/upgrade-check", request, &UpgradeCheckResponse{})
}
