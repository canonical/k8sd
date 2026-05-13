package preflight

// Verdict is the upgrade safety verdict for a single component or the whole upgrade.
type Verdict string

const (
	VerdictPass  Verdict = "pass"
	VerdictWarn  Verdict = "warn"
	VerdictBlock Verdict = "block"
)

// ComponentDelta represents a version change for a single component.
type ComponentDelta struct {
	Name        string `json:"name"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	RepoURL     string `json:"repo_url"`
}

// ComponentInfo holds a component's name, version, and upstream repo URL.
type ComponentInfo struct {
	Name    string
	Version string
	RepoURL string
}

// Warning describes an upgrade concern for a component.
type Warning struct {
	Severity  string `json:"severity"` // "info", "warning", "critical"
	Component string `json:"component"`
	Message   string `json:"message"`
	SourceURL string `json:"source_url,omitempty"`
}

// Remediation describes a required or recommended action.
type Remediation struct {
	Component     string `json:"component"`
	Description   string `json:"description"`
	BeforeUpgrade bool   `json:"before_upgrade"`
}

// ComponentResult holds the analysis for a single component version change.
type ComponentResult struct {
	Delta        ComponentDelta `json:"delta"`
	Verdict      Verdict        `json:"verdict"`
	Warnings     []Warning      `json:"warnings"`
	Remediations []Remediation  `json:"remediations"`
}

// PreflightResult holds the full upgrade preflight analysis.
type PreflightResult struct {
	FromChannel string            `json:"from_channel"`
	ToChannel   string            `json:"to_channel"`
	Verdict     Verdict           `json:"verdict"`
	Components  []ComponentResult `json:"components"`
	Summary     string            `json:"summary"`
}
