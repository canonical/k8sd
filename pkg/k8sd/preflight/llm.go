package preflight

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Analyzer evaluates upgrade safety for a single component version delta.
type Analyzer interface {
	AnalyzeComponent(ctx context.Context, delta ComponentDelta) (ComponentResult, error)
}

// MockAnalyzer returns deterministic results based on simple version comparison rules.
type MockAnalyzer struct{}

// AnalyzeComponent classifies a component version change.
func (m *MockAnalyzer) AnalyzeComponent(ctx context.Context, delta ComponentDelta) (ComponentResult, error) {
	result := ComponentResult{Delta: delta}

	if delta.FromVersion == delta.ToVersion || delta.FromVersion == "" {
		result.Verdict = VerdictPass
		return result, nil
	}

	fromParts := parseVersion(delta.FromVersion)
	toParts := parseVersion(delta.ToVersion)

	if len(fromParts) >= 2 && len(toParts) >= 2 {
		if toParts[0] > fromParts[0] {
			result.Verdict = VerdictBlock
			result.Warnings = append(result.Warnings, Warning{
				Severity:  "critical",
				Component: delta.Name,
				Message: fmt.Sprintf(
					"Major version upgrade from %s to %s. Sequential minor version upgrades required.",
					delta.FromVersion, delta.ToVersion,
				),
			})
			return result, nil
		}

		if toParts[1] > fromParts[1] && isCriticalComponent(delta.Name) {
			result.Verdict = VerdictWarn
			result.Warnings = append(result.Warnings, Warning{
				Severity:  "warning",
				Component: delta.Name,
				Message: fmt.Sprintf(
					"Minor version upgrade for %s from %s to %s. Review release notes for breaking changes and deprecated APIs.",
					delta.Name, delta.FromVersion, delta.ToVersion,
				),
			})
			return result, nil
		}
	}

	result.Verdict = VerdictPass
	return result, nil
}

// isCriticalComponent returns true for infrastructure components where minor bumps carry risk.
func isCriticalComponent(name string) bool {
	switch name {
	case "kubernetes", "etcd", "containerd", "runc", "cni":
		return true
	default:
		return false
	}
}

// parseVersion extracts numeric parts from a version string like "v1.35.3" or "1.18.4-ck0".
func parseVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	// Strip build metadata (e.g., "-ck0", "+incompatible")
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	var nums []int
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			nums = append(nums, n)
		} else {
			break
		}
	}
	return nums
}
