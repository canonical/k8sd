package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/client/k8sd"
	"github.com/spf13/cobra"
)

type UpgradeCheckResult struct {
	FromVersion string                         `json:"from_version" yaml:"from_version"`
	ToVersion   string                         `json:"to_version" yaml:"to_version"`
	Verdict     string                         `json:"verdict" yaml:"verdict"`
	Components  []k8sd.UpgradeComponentResult  `json:"components" yaml:"components"`
	Summary     string                         `json:"summary" yaml:"summary"`
}

func verdictBadge(v string) string {
	switch v {
	case "pass":
		return "PASS"
	case "warn":
		return "WARN"
	case "blocked":
		return "BLOCKED"
	default:
		return strings.ToUpper(v)
	}
}

func (r UpgradeCheckResult) String() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Upgrade check: %s → %s", r.FromVersion, r.ToVersion))
	sb.WriteString(fmt.Sprintf("  [%s]\n", verdictBadge(r.Verdict)))

	maxNameLen := 10
	maxFromLen := 12
	maxToLen := 10
	for _, c := range r.Components {
		if len(c.Name) > maxNameLen {
			maxNameLen = len(c.Name)
		}
		if len(c.FromVersion) > maxFromLen {
			maxFromLen = len(c.FromVersion)
		}
		if len(c.ToVersion) > maxToLen {
			maxToLen = len(c.ToVersion)
		}
	}

	nameFmt := fmt.Sprintf("%%-%ds", maxNameLen)
	fromFmt := fmt.Sprintf("%%-%ds", maxFromLen)
	toFmt := fmt.Sprintf("%%-%ds", maxToLen)

	divider := fmt.Sprintf("+%s+%s+%s+-----+\n",
		strings.Repeat("-", maxNameLen+2),
		strings.Repeat("-", maxFromLen+2),
		strings.Repeat("-", maxToLen+2))

	sb.WriteString(divider)
	sb.WriteString(fmt.Sprintf("| "+nameFmt+" | "+fromFmt+" | "+toFmt+" | Verdict |\n", "Component", "From", "To"))
	sb.WriteString(divider)

	for _, c := range r.Components {
		sb.WriteString(fmt.Sprintf("| "+nameFmt+" | "+fromFmt+" | "+toFmt+" | %-7s |\n",
			c.Name, c.FromVersion, c.ToVersion, verdictBadge(c.Verdict)))
	}
	sb.WriteString(divider)

	for _, c := range r.Components {
		for _, w := range c.Warnings {
			sb.WriteString(fmt.Sprintf("\n  [%s] %s: %s\n", strings.ToUpper(w.Severity), c.Name, w.Message))
		}
	}

	if r.Summary != "" {
		sb.WriteString(fmt.Sprintf("\n%s\n", r.Summary))
	}

	return sb.String()
}

func newUpgradeCheckCmd(env cmdutil.ExecutionEnvironment) *cobra.Command {
	var opts struct {
		fromVersion  string
		toVersion    string
		outputFormat string
		timeout      time.Duration
	}
	cmd := &cobra.Command{
		Use:    "upgrade-check",
		Short:  "Check upgrade compatibility from a source version to a target version",
		Long:   "Check if the current Kubernetes cluster can be upgraded from the specified source version to the target version.",
		Args:   cobra.NoArgs,
		PreRun: chainPreRunHooks(hookRequireRoot(env), hookInitializeFormatter(env, &opts.outputFormat)),
		Run: func(cmd *cobra.Command, args []string) {
			if opts.timeout < minTimeout {
				cmd.PrintErrf("Timeout %v is less than minimum of %v. Using the minimum %v instead.\n", opts.timeout, minTimeout, minTimeout)
				opts.timeout = minTimeout
			}

			if opts.fromVersion == "" {
				cmd.PrintErrln("Error: --from-version must not be empty.")
				env.Exit(1)
				return
			}
			if opts.toVersion == "" {
				cmd.PrintErrln("Error: --to-version must not be empty.")
				env.Exit(1)
				return
			}

			client, err := env.Snap.K8sdClient("")
			if err != nil {
				cmd.PrintErrf("Error: Failed to create a k8sd client. Make sure that the k8sd service is running.\n\nThe error was: %v\n", err)
				env.Exit(1)
				return
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			cobra.OnFinalize(cancel)

			request := k8sd.UpgradeCheckRequest{
				FromVersion: opts.fromVersion,
				ToVersion:   opts.toVersion,
			}

			response, err := client.UpgradeCheck(ctx, request)
			if err != nil {
				cmd.PrintErrf("Error: Failed to check upgrade from %q to %q.\n\nThe error was: %v\n", opts.fromVersion, opts.toVersion, err)
				env.Exit(1)
				return
			}

			outputFormatter.Print(UpgradeCheckResult{
				FromVersion: response.FromVersion,
				ToVersion:   response.ToVersion,
				Verdict:     response.Verdict,
				Components:  response.Components,
				Summary:     response.Summary,
			})
		},
	}

	cmd.Flags().StringVar(&opts.fromVersion, "from-version", "", "the version to upgrade from")
	cmd.Flags().StringVar(&opts.toVersion, "to-version", "", "the version to upgrade to")
	cmd.Flags().StringVar(&opts.outputFormat, "output-format", "plain", "set the output format to one of plain, json or yaml")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 90*time.Second, "the max time to wait for the command to execute")

	_ = cmd.MarkFlagRequired("from-version")
	_ = cmd.MarkFlagRequired("to-version")

	return cmd
}