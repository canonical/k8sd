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
	FromChannel string                         `json:"from_channel" yaml:"from_channel"`
	ToChannel   string                         `json:"to_channel" yaml:"to_channel"`
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

	sb.WriteString(fmt.Sprintf("Upgrade check: %s → %s", r.FromChannel, r.ToChannel))
	sb.WriteString(fmt.Sprintf("  [%s]\n", verdictBadge(r.Verdict)))

	maxNameLen := len("Component")
	maxFromLen := len("From")
	maxToLen := len("To")
	maxVerdictLen := len("Verdict")
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
		if v := verdictBadge(c.Verdict); len(v) > maxVerdictLen {
			maxVerdictLen = len(v)
		}
	}

	nameFmt := fmt.Sprintf("%%-%ds", maxNameLen)
	fromFmt := fmt.Sprintf("%%-%ds", maxFromLen)
	toFmt := fmt.Sprintf("%%-%ds", maxToLen)
	verdictFmt := fmt.Sprintf("%%-%ds", maxVerdictLen)

	divider := fmt.Sprintf("+%s+%s+%s+%s+\n",
		strings.Repeat("-", maxNameLen+2),
		strings.Repeat("-", maxFromLen+2),
		strings.Repeat("-", maxToLen+2),
		strings.Repeat("-", maxVerdictLen+2))

	sb.WriteString(divider)
	sb.WriteString(fmt.Sprintf("| "+nameFmt+" | "+fromFmt+" | "+toFmt+" | "+verdictFmt+" |\n", "Component", "From", "To", "Verdict"))
	sb.WriteString(divider)

	for _, c := range r.Components {
		sb.WriteString(fmt.Sprintf("| "+nameFmt+" | "+fromFmt+" | "+toFmt+" | "+verdictFmt+" |\n",
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
		fromChannel  string
		toChannel    string
		outputFormat string
		timeout      time.Duration
	}
	cmd := &cobra.Command{
		Use:    "upgrade-check",
		Short:  "Check upgrade compatibility between snap channels",
		Long:   "Check if the current Kubernetes cluster can be safely upgraded to the target snap channel.",
		Args:   cobra.NoArgs,
		PreRun: chainPreRunHooks(hookRequireRoot(env), hookInitializeFormatter(env, &opts.outputFormat)),
		Run: func(cmd *cobra.Command, args []string) {
			if opts.timeout < minTimeout {
				cmd.PrintErrf("Timeout %v is less than minimum of %v. Using the minimum %v instead.\n", opts.timeout, minTimeout, minTimeout)
				opts.timeout = minTimeout
			}

			if opts.toChannel == "" {
				cmd.PrintErrln("Error: --to-channel must not be empty.")
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
				FromChannel: opts.fromChannel,
				ToChannel:   opts.toChannel,
			}

			response, err := client.UpgradeCheck(ctx, request)
			if err != nil {
				cmd.PrintErrf("Error: Failed to check upgrade from %q to %q.\n\nThe error was: %v\n", opts.fromChannel, opts.toChannel, err)
				env.Exit(1)
				return
			}

			outputFormatter.Print(UpgradeCheckResult{
				FromChannel: response.FromChannel,
				ToChannel:   response.ToChannel,
				Verdict:     response.Verdict,
				Components:  response.Components,
				Summary:     response.Summary,
			})
		},
	}

	cmd.Flags().StringVar(&opts.fromChannel, "from-channel", "", "the snap channel to compare from (default: current snap channel)")
	cmd.Flags().StringVar(&opts.toChannel, "to-channel", "", "the snap channel to upgrade to")
	cmd.Flags().StringVar(&opts.outputFormat, "output-format", "plain", "set the output format to one of plain, json or yaml")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 90*time.Second, "the max time to wait for the command to execute")

	_ = cmd.MarkFlagRequired("to-channel")

	return cmd
}