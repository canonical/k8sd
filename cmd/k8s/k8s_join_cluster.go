package k8s

import (
	"fmt"
	"io"
	"os"
	"time"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/config"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"
)

type JoinClusterResult struct {
	Name string `json:"name" yaml:"name"`
}

func (b JoinClusterResult) String() string {
	return fmt.Sprintf("Cluster services have started on %q.\nPlease allow some time for initial Kubernetes node registration.\n", b.Name)
}

func newJoinClusterCmd(env cmdutil.ExecutionEnvironment) *cobra.Command {
	var opts struct {
		name              string
		address           string
		configFile        string
		outputFormat      string
		timeout           time.Duration
		containerdBaseDir string
	}
	cmd := &cobra.Command{
		Use:    "join-cluster <join-token>",
		Short:  "Join a cluster using the provided token",
		Long:   "Join a new node to an existing Kubernetes cluster using a join token obtained from a control plane node.",
		PreRun: chainPreRunHooks(hookRequireRoot(env), hookInitializeFormatter(env, &opts.outputFormat), hookCheckLXD()),
		Args:   cmdutil.ExactArgs(env, 1),
		Run: func(cmd *cobra.Command, args []string) {
			token := args[0]

			if opts.timeout < minTimeout {
				cmd.PrintErrf("Timeout %v is less than minimum of %v. Using the minimum %v instead.\n", opts.timeout, minTimeout, minTimeout)
				opts.timeout = minTimeout
			}

			// Use hostname as default node name
			if opts.name == "" {
				// TODO(neoaggelos): use the encoded node name from the token, if available.
				hostname, err := os.Hostname()
				if err != nil {
					cmd.PrintErrf("Error: --name is not set and could not determine the current node name.\n\nThe error was: %v\n", err)
					env.Exit(1)
					return
				}
				opts.name = hostname
			}

			address, err := utils.ParseAddressString(opts.address, config.DefaultPort)
			if err != nil {
				cmd.PrintErrf("Error: Failed to parse the address %q.\n\nThe error was: %v\n", opts.address, err)
				env.Exit(1)
				return
			}

			client, err := env.Snap.K8sdClient("")
			if err != nil {
				cmd.PrintErrf("Error: Failed to create a k8sd client. Make sure that the k8sd service is running.\n\nThe error was: %v\n", err)
				env.Exit(1)
				return
			}

			if _, initialized, err := client.NodeStatus(cmd.Context()); err != nil {
				cmd.PrintErrf("Error: Failed to check the current node status.\n\nThe error was: %v\n", err)
				env.Exit(1)
				return
			} else if initialized {
				cmd.PrintErrln("Error: The node is already part of a cluster")
				env.Exit(1)
				return
			}

			var joinClusterConfig string
			if opts.configFile != "" {
				var b []byte
				var err error

				if opts.configFile == "-" {
					b, err = io.ReadAll(os.Stdin)
					if err != nil {
						cmd.PrintErrf("Error: Failed to read join configuration from stdin. \n\nThe error was: %v\n", err)
						env.Exit(1)
						return
					}
				} else {
					b, err = os.ReadFile(opts.configFile)
					if err != nil {
						cmd.PrintErrf("Error: Failed to read join configuration from %q.\n\nThe error was: %v\n", opts.configFile, err)
						env.Exit(1)
						return
					}
				}
				joinClusterConfig = string(b)
			}

			if opts.containerdBaseDir != "" {
				var joinConfigMap map[string]any
				if joinClusterConfig != "" {
					if err := yaml.Unmarshal([]byte(joinClusterConfig), &joinConfigMap); err != nil {
						cmd.PrintErrf("Error: Failed to parse join configuration.\n\nThe error was: %v\n", err)
						env.Exit(1)
						return
					}
				}
				if joinConfigMap == nil {
					joinConfigMap = map[string]any{}
				}

				normalizedDir, err := normalizeContainerdBaseDir(opts.containerdBaseDir)
				if err != nil {
					cmd.PrintErrf("Error: invalid containerd-base-dir value.\n\nThe error was: %v\n", err)
					env.Exit(1)
					return
				}

				if isMemBackedFS(normalizedDir) {
					cmd.PrintErrln("Warning: containerd-base-dir is on a memory-backed filesystem (tmpfs/ramfs). Containerd state (images, containers) will be lost on reboot.")
				}

				joinConfigMap["containerd-base-dir"] = normalizedDir
				b, err := yaml.Marshal(joinConfigMap)
				if err != nil {
					cmd.PrintErrf("Error: Failed to serialize join configuration.\n\nThe error was: %v\n", err)
					env.Exit(1)
					return
				}
				joinClusterConfig = string(b)
			}

			if err := verifyJoinConfig(joinClusterConfig, token); err != nil {
				cmd.PrintErrf("Join cluster config verification failed: %v", err)
				env.Exit(1)
				return
			}

			cmd.PrintErrln("Joining the cluster. This may take a few seconds, please wait.")
			if err := client.JoinCluster(cmd.Context(), apiv2.JoinClusterRequest{
				Name:    opts.name,
				Address: address,
				Token:   token,
				Config:  joinClusterConfig,
				Timeout: opts.timeout,
			}); err != nil {
				cmd.PrintErrf("Error: Failed to join the cluster using the provided token.\n\nThe error was: %v\n", err)
				env.Exit(1)
				return
			}

			outputFormatter.Print(JoinClusterResult{Name: opts.name})
		},
	}
	cmd.Flags().StringVar(&opts.name, "name", "", "node name, defaults to hostname")
	cmd.Flags().StringVar(&opts.address, "address", "", "microcluster address or CIDR, defaults to the node IP address")
	cmd.Flags().StringVar(&opts.configFile, "file", "", "path to the YAML file containing your custom cluster join configuration. Use '-' to read from stdin.")
	cmd.Flags().StringVar(&opts.outputFormat, "output-format", "plain", "set the output format to one of plain, json or yaml")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 90*time.Second, "the max time to wait for the command to execute")
	cmd.Flags().StringVar(&opts.containerdBaseDir, "containerd-base-dir", "", "set a dedicated absolute base directory for containerd")

	return cmd
}
