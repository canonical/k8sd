package preflight

import (
	"context"
	"fmt"
	"strings"
)

// Checker orchestrates the upgrade preflight analysis.
type Checker struct {
	Analyzer       Analyzer
	SnapDownloader *SnapDownloader
}

// NewChecker creates a Checker with the given analyzer and downloader.
func NewChecker(analyzer Analyzer, downloader *SnapDownloader) *Checker {
	return &Checker{
		Analyzer:       analyzer,
		SnapDownloader: downloader,
	}
}

// CheckTargetChannel compares the current snap against a target channel.
func (c *Checker) CheckTargetChannel(ctx context.Context, fromChannel, toChannel string) (*PreflightResult, error) {
	current := CurrentComponents()

	targetComponents, _, err := c.SnapDownloader.DownloadTargetSnap(ctx, toChannel)
	if err != nil {
		return nil, fmt.Errorf("failed to download target snap: %w", err)
	}

	return c.compare(ctx, current, targetComponents, fromChannel, toChannel)
}

// CheckTargetImages compares the current snap against a known target image list.
func (c *Checker) CheckTargetImages(ctx context.Context, fromChannel string, targetImages []string) (*PreflightResult, error) {
	current := CurrentComponents()
	target := ParseImageLines(targetImages)
	return c.compare(ctx, current, target, fromChannel, "provided-images")
}

func (c *Checker) compare(ctx context.Context, current, target []ComponentInfo, fromChannel, toChannel string) (*PreflightResult, error) {
	currentMap := make(map[string]string)
	for _, comp := range current {
		currentMap[comp.Name] = comp.Version
	}

	var components []ComponentResult
	for _, t := range target {
		fromVer := currentMap[t.Name]
		delta := ComponentDelta{
			Name:        t.Name,
			FromVersion: fromVer,
			ToVersion:   t.Version,
			RepoURL:     t.RepoURL,
		}

		result, err := c.Analyzer.AnalyzeComponent(ctx, delta)
		if err != nil {
			return nil, fmt.Errorf("failed to analyze component %s: %w", t.Name, err)
		}
		components = append(components, result)
	}

	return aggregateResults(fromChannel, toChannel, components), nil
}

// aggregateResults produces a final PreflightResult from individual component results.
func aggregateResults(fromChannel, toChannel string, components []ComponentResult) *PreflightResult {
result := &PreflightResult{
		FromChannel: fromChannel,
		ToChannel:   toChannel,
		Verdict:     VerdictPass,
		Components:  components,
	}

	if len(components) == 0 {
		result.Verdict = VerdictWarn
		result.Summary = "No components found for comparison. The snap images.txt may be empty or contain unrecognized image paths."
		return result
	}

	var (
		warnCount  int
		blockCount int
	)

	for _, c := range components {
		switch c.Verdict {
		case VerdictBlock:
			blockCount++
			result.Verdict = VerdictBlock
		case VerdictWarn:
			warnCount++
			if result.Verdict == VerdictPass {
				result.Verdict = VerdictWarn
			}
		}
	}

	parts := []string{}
	if blockCount > 0 {
		parts = append(parts, fmt.Sprintf("%d blocker(s)", blockCount))
	}
	if warnCount > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", warnCount))
	}
	if len(parts) == 0 {
		result.Summary = "No issues found. Upgrade appears safe."
	} else {
		result.Summary = strings.Join(parts, ", ")
	}

	return result
}
