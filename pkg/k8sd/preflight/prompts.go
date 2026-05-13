package preflight

import "fmt"

// Prompt templates for LLM-based upgrade analysis.
// These are placeholder constants. When a real LLM backend is integrated,
// these templates will be used to construct the prompts.

const systemPrompt = `You are a Kubernetes upgrade safety analyzer. Your task: evaluate whether
upgrading a specific component from version A to version B is safe.

For each component, you will receive:
- Component name and repository
- Current version and target version
- Release notes or changelog for versions between A and B
- Relevant Kubernetes release notes for the target K8s version

Analyze for:
1. BREAKING CHANGES: API removals, flag deprecations, config format changes
2. DEPRECATIONS: features/APIs deprecated in the target version
3. COMPATIBILITY: does the new component version work with the current K8s version?
4. MIGRATION STEPS: any manual steps required before or after upgrade
5. KNOWN ISSUES: regressions or bugs mentioned in release notes

Output as JSON matching the ComponentResult schema.

VERDICT RULES:
- "block" if there are breaking changes requiring manual migration before upgrade
- "warn" if there are deprecations or configuration changes to be aware of
- "pass" if no issues found (patch bump only, or no relevant changes)`

const componentPromptTemplate = `Component: %s
Current version: %s
Target version: %s
Repository: %s

Component Release Notes:
%s

Kubernetes Release Notes (for target K8s version):
%s

Analyze the upgrade safety for this component. Return JSON.`

// FormatComponentPrompt builds the per-component LLM prompt.
func FormatComponentPrompt(name, fromVer, toVer, repoURL, componentNotes, k8sNotes string) string {
	return fmt.Sprintf(componentPromptTemplate, name, fromVer, toVer, repoURL, componentNotes, k8sNotes)
}
