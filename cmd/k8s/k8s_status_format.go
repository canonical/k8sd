package k8s

import (
	"fmt"
	"strings"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/fatih/color"
)

type ClusterStatus apiv2.ClusterStatus

// TICS -COV_GO_SUPPRESSED_ERROR
// We are just formatting the output for the k8s status command; it is ok to
// ignore failures from strings.Builder writes.

// String renders the human-readable plain output of `k8s status`.
//
// Layout:
//
//	<cluster-icon> cluster: <status> [(<ha-qualifier>)]
//	  nodes: <N> control-plane[ (<X> unreachable)], <M> worker
//
//	Networking:
//	  <icon> <feature> [(<component> <version>)]
//	      <human readable description>
//	  ...
//
//	Storage:
//	  ...
//
//	Observability:
//	  ...
//
//	Suggestions:
//	    <command>    <description>
func (c ClusterStatus) String() string {
	cs := apiv2.ClusterStatus(c)

	var b strings.Builder
	b.WriteString(renderClusterHeader(cs))
	b.WriteString("\n")
	b.WriteString(renderNodeCounts(cs.Members))
	b.WriteString("\n\n")

	for _, section := range featureSections(cs) {
		b.WriteString(renderSection(section))
		b.WriteString("\n")
	}

	if s := formatSuggestions(suggestionsFor(cs)); s != "" {
		b.WriteString(s)
	}

	return strings.TrimRight(b.String(), "\n")
}

// FormatUnbootstrapped returns the human-readable message shown when
// `k8s status` runs on a node that is not part of any cluster.
func FormatUnbootstrapped() string {
	var b strings.Builder
	b.WriteString("This node is not part of a Kubernetes cluster.\n\n")
	b.WriteString(formatSuggestions([]suggestion{
		{cmd: "k8s bootstrap", desc: "Bootstrap a new Kubernetes cluster"},
		{cmd: "k8s join-cluster <token>", desc: "Join an existing cluster. Requires a token created by running `k8s get-join-token` on a cluster member."},
	}))
	return b.String()
}

// TICS +COV_GO_SUPPRESSED_ERROR

// -----------------------------------------------------------------------------
// Icons & styles
// -----------------------------------------------------------------------------

var (
	styleBold = color.New(color.Bold)
	styleDim  = color.New(color.Faint)
)

// Cluster-level icons.
func iconClusterReady() string    { return "✓" }
func iconClusterFailed() string   { return styleBold.Sprint("✘") }
func iconClusterDegraded() string { return fmt.Sprint("⚠") }

// Feature-level icons.
func iconFeatureHealthy() string  { return "●" }
func iconFeatureFailed() string   { return styleBold.Sprint("✘") }
func iconFeatureDegraded() string { return fmt.Sprint("⚠") }
func iconFeatureDisabled() string { return styleDim.Sprint("○") }

// -----------------------------------------------------------------------------
// Cluster header & node counts
// -----------------------------------------------------------------------------

func renderClusterHeader(c apiv2.ClusterStatus) string {
	health := effectiveHealth(c)

	var icon, label string
	switch health {
	case apiv2.ClusterHealthReady:
		icon, label = iconClusterReady(), "ready"
	case apiv2.ClusterHealthDegraded:
		icon = iconClusterDegraded()
		label = "degraded"
	default: // ClusterHealthFailed or unknown
		icon, label = iconClusterFailed(), "not ready"
	}

	if c.IsHA {
		label += " (high-availability)"
	}

	return fmt.Sprintf("%s cluster: %s", icon, label)
}

func renderNodeCounts(members []apiv2.NodeStatus) string {
	cp, worker, cpUnreachable, workerUnreachable := countNodes(members)

	cpPart := fmt.Sprintf("%d control-plane", cp)
	if cpUnreachable > 0 {
		cpPart += fmt.Sprintf(" (%d unreachable)", cpUnreachable)
	}

	workerPart := fmt.Sprintf("%d worker", worker)
	if workerUnreachable > 0 {
		workerPart += fmt.Sprintf(" (%d unreachable)", workerUnreachable)
	}

	if worker == 0 {
		return fmt.Sprintf("  nodes: %s", cpPart)
	}

	return fmt.Sprintf("  nodes: %s, %s", cpPart, workerPart)
}

// effectiveHealth returns c.Status when set, falling back to c.Ready for
// backwards compatibility with older server responses.
func effectiveHealth(c apiv2.ClusterStatus) apiv2.ClusterHealth {
	if c.Status != "" {
		return c.Status
	}
	if c.Ready {
		return apiv2.ClusterHealthReady
	}
	return apiv2.ClusterHealthFailed
}

func countNodes(members []apiv2.NodeStatus) (cp, worker, cpUnreachable, workerUnreachable int) {
	for _, m := range members {
		switch m.ClusterRole {
		case apiv2.ClusterRoleControlPlane:
			cp++
			if !m.Reachable {
				cpUnreachable++
			}
		case apiv2.ClusterRoleWorker:
			worker++
			if !m.Reachable {
				workerUnreachable++
			}
		}
	}
	return cp, worker, cpUnreachable, workerUnreachable
}

func hasUnreachableControlPlane(members []apiv2.NodeStatus) bool {
	for _, m := range members {
		if m.ClusterRole == apiv2.ClusterRoleControlPlane && !m.Reachable {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Feature sections
// -----------------------------------------------------------------------------

type featureRow struct {
	name   string
	status apiv2.FeatureStatus
}

type categorySection struct {
	title string
	rows  []featureRow
}

// featureSections returns the categorised list of features in the documented
// display order.
func featureSections(c apiv2.ClusterStatus) []categorySection {
	return []categorySection{
		{
			title: "Networking",
			rows: []featureRow{
				{"network", c.Network},
				{"dns", c.DNS},
				{"load-balancer", c.LoadBalancer},
				{"ingress", c.Ingress},
				{"gateway", c.Gateway},
			},
		},
		{
			title: "Storage",
			rows: []featureRow{
				{"local-storage", c.LocalStorage},
			},
		},
		{
			title: "Observability",
			rows: []featureRow{
				{"metrics-server", c.MetricsServer},
			},
		},
	}
}

func renderSection(s categorySection) string {
	var b strings.Builder
	b.WriteString(s.title)
	b.WriteString(":\n")
	for i, row := range s.rows {
		b.WriteString(renderFeature(row.name, row.status, i == len(s.rows)-1))
		b.WriteString("\n")
	}
	return b.String()
}

func renderFeature(name string, fs apiv2.FeatureStatus, last bool) string {
	state := fs.State
	if state == apiv2.FeatureStateDisabled {
		return fmt.Sprintf("  %s %s", iconFeatureDisabled(), styleDim.Sprint(name))
	}

	icon := iconFeatureHealthy()
	switch fs.State {
	case apiv2.FeatureStateFailed:
		icon = iconFeatureFailed()
	case apiv2.FeatureStateDegraded, apiv2.FeatureStateWaiting:
		icon = iconFeatureDegraded()
	}

	header := fmt.Sprintf("  %s %s", icon, name)
	if qualifier := componentQualifier(fs); qualifier != "" {
		header += " " + qualifier
	}

	header += "\n      " + fs.Message

	if (state == apiv2.FeatureStateEnabled && !last) || !last {
		return header + "\n"
	}

	return header
}

// componentQualifier renders the "(<component> <version>)" suffix shown after
// a feature name. Returns an empty string when neither field is populated.
func componentQualifier(fs apiv2.FeatureStatus) string {
	switch {
	case fs.Component != "" && fs.Version != "":
		if fs.Version[0] != 'v' {
			return fmt.Sprintf("(%s v%s)", fs.Component, fs.Version)
		}
		return fmt.Sprintf("(%s %s)", fs.Component, fs.Version)
	case fs.Component != "":
		return fmt.Sprintf("(%s)", fs.Component)
	case fs.Version != "":
		return fmt.Sprintf("(%s)", fs.Version)
	}
	return ""
}

// -----------------------------------------------------------------------------
// Suggestions
// -----------------------------------------------------------------------------

type suggestion struct {
	cmd  string
	desc string
}

// suggestionsFor picks the contextual suggestions block for the given cluster
// state. Today only the ready branch is exercised end-to-end; the other
// branches are stubbed for upcoming unhappy-path work.
func suggestionsFor(c apiv2.ClusterStatus) []suggestion {
	switch effectiveHealth(c) {
	case apiv2.ClusterHealthReady:
		return []suggestion{
			{cmd: "k8s kubectl get nodes", desc: "View detailed node information"},
			{cmd: "k8s get", desc: "View cluster configuration"},
		}
	case apiv2.ClusterHealthDegraded, apiv2.ClusterHealthFailed:
		return []suggestion{
			{cmd: "k8s inspect", desc: "Collect logs and debug information"},
		}
	}
	return nil
}

// formatSuggestions renders a Suggestions block with command and description
// columns aligned to the longest command in the group.
func formatSuggestions(ss []suggestion) string {
	if len(ss) == 0 {
		return ""
	}

	maxCmd := 0
	for _, s := range ss {
		if n := len(s.cmd); n > maxCmd {
			maxCmd = n
		}
	}

	var b strings.Builder
	b.WriteString("Suggestions:\n")
	for _, s := range ss {
		b.WriteString(fmt.Sprintf("    %-*s    %s\n", maxCmd, s.cmd, s.desc))
	}
	return strings.TrimRight(b.String(), "\n")
}
