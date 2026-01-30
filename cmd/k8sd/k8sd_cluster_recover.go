package k8sd

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/termios"
	"github.com/canonical/microcluster/v2/cluster"
	"github.com/canonical/microcluster/v2/microcluster"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"
)

const preRecoveryMessage = `You should only run this command if:
 - A quorum of cluster members is permanently lost
 - You are *absolutely* sure all k8s daemons are stopped (sudo snap stop k8s)
 - This instance has the most up to date database

Note that before applying any changes, a database backup is created at:
* k8sd (microcluster): /var/snap/k8s/common/var/lib/k8sd/state/db_backup.<timestamp>.tar.gz
`

const recoveryConfirmation = "Do you want to proceed? (yes/no): "

const nonInteractiveMessage = `Non-interactive mode requested.

The command will assume that the dqlite configuration files have already been
modified with the updated cluster member roles and addresses.

Initiating the dqlite database recovery.
`

const clusterK8sdYamlRecoveryComment = `# Member roles can be modified. Unrecoverable nodes should be given the role "spare".
#
# "voter" (0) - Voting member of the database. A majority of voters is a quorum.
# "stand-by" (1) - Non-voting member of the database; can be promoted to voter.
# "spare" (2) - Not a member of the database.
#
# The edit is aborted if:
# - the number of members changes
# - the name of any member changes
# - the ID of any member changes
# - the address of any member changes
# - no changes are made
`

const infoYamlRecoveryComment = `# Verify the ID, address and role of the local node.
#
# Cluster members:
`

const daemonYamlRecoveryComment = `# Verify the name and address of the local node.
#
# Cluster members:
`

// Used as part of a regex search, avoid adding special characters.
const yamlHelperCommentFooter = "# ------- everything below will be written -------\n"

var clusterRecoverOpts struct {
	NonInteractive bool
}

func newClusterRecoverCmd(env cmdutil.ExecutionEnvironment) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-recover",
		Short: "Recover the cluster from this member if quorum is lost",
		Run: func(cmd *cobra.Command, args []string) {
			log.Configure(log.Options{
				LogLevel:     rootCmdOpts.logLevel,
				AddDirHeader: true,
			})

			if err := recoveryCmdPrechecks(cmd); err != nil {
				cmd.PrintErrf("Recovery precheck failed: %v\n", err)
				env.Exit(1)
			}

			k8sdTarballPath, err := recoverK8sd()
			if err != nil {
				cmd.PrintErrf("Failed to recover k8sd, error: %v\n", err)
				env.Exit(1)
			}
			cmd.Printf("K8sd cluster changes applied.\n")
			cmd.Printf("New database state saved to %s\n", k8sdTarballPath)
			cmd.Printf("*Before* starting any cluster member, copy %s to %s "+
				"on all remaining cluster members.\n",
				k8sdTarballPath, k8sdTarballPath)
			cmd.Printf("K8sd will load this file during startup.\n\n")
		},
	}

	cmd.Flags().BoolVar(&clusterRecoverOpts.NonInteractive, "non-interactive",
		false, "disable interactive prompts, assume that the configs have been updated")

	return cmd
}

func removeYamlHelperComments(content []byte) []byte {
	pattern := fmt.Sprintf("(?s).*?%s *", yamlHelperCommentFooter)
	re := regexp.MustCompile(pattern)
	out := re.ReplaceAll(content, nil)
	return out
}

func removeEmptyLines(content []byte) []byte {
	re := regexp.MustCompile(`(?m)^\s*$`)
	out := re.ReplaceAll(content, nil)
	return out
}

func recoveryCmdPrechecks(cmd *cobra.Command) error {
	log := log.FromContext(cmd.Context())

	log.V(1).Info("Running prechecks.")

	if !termios.IsTerminal(unix.Stdin) && !clusterRecoverOpts.NonInteractive {
		return fmt.Errorf("interactive mode requested in a non-interactive terminal")
	}

	if rootCmdOpts.stateDir == "" {
		return fmt.Errorf("k8sd state dir not specified")
	}

	cmd.Print(preRecoveryMessage)
	cmd.Print("\n")

	if clusterRecoverOpts.NonInteractive {
		cmd.Print(nonInteractiveMessage)
		cmd.Print("\n")
	} else {
		reader := bufio.NewReader(os.Stdin)
		cmd.Print(recoveryConfirmation)

		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("couldn't read user input, error: %w", err)
		}
		input = strings.TrimSuffix(input, "\n")

		if strings.ToLower(input) != "yes" {
			return fmt.Errorf("cluster edit aborted; no changes made")
		}

		cmd.Print("\n")
	}

	return nil
}

// yamlEditorGuide is a convenience wrapper around shared.TextEditor
// that passes the current file contents prepended by the guide contents,
// which are meant to assist the user. Returns the user-edited file contents.
// If applyChanges is set, the changes made by the user are applied to the file.
func yamlEditorGuide(
	path string,
	readFile bool,
	guideContent []byte,
	applyChanges bool,
) ([]byte, error) {
	currContent := []byte{}
	var err error
	if readFile {
		currContent, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("could not read file: %s, error: %w", path, err)
		}
		currContent = removeEmptyLines(currContent)
	}

	textEditorContent := slices.Concat(
		[]byte(guideContent),
		[]byte("\n"),
		currContent)
	newContent, err := shared.TextEditor("", textEditorContent)
	if err != nil {
		return nil, fmt.Errorf("text editor failed, error: %w", err)
	}

	newContent = removeYamlHelperComments(newContent)
	newContent = removeEmptyLines(newContent)

	if applyChanges {
		err = utils.WriteFile(path, newContent, os.FileMode(0o644))
		if err != nil {
			return nil, fmt.Errorf("could not write file: %s, error: %w", path, err)
		}
	}

	return newContent, err
}

// On success, returns the recovery tarball path.
func recoverK8sd() (string, error) {
	m, err := microcluster.App(
		microcluster.Args{
			StateDir: rootCmdOpts.stateDir,
		},
	)
	if err != nil {
		return "", fmt.Errorf("could not initialize microcluster app, error: %w", err)
	}

	// The following method parses cluster.yaml and filters out the entries
	// that are not included in the trust store.
	members, err := m.GetDqliteClusterMembers()
	if err != nil {
		return "", fmt.Errorf("could not retrieve K8sd cluster members, error: %w", err)
	}

	oldMembersYaml, err := yaml.Marshal(members)
	if err != nil {
		return "", fmt.Errorf("could not serialize cluster members, error: %w", err)
	}

	clusterYamlPath := path.Join(m.FileSystem.DatabaseDir, "cluster.yaml")
	clusterYamlCommentHeader := fmt.Sprintf("# K8sd cluster configuration\n# (based on the trust store and %s)\n", clusterYamlPath)

	clusterYamlContent := oldMembersYaml
	if !clusterRecoverOpts.NonInteractive {
		// Interactive mode requested (default).
		// Assist the user in configuring dqlite.
		clusterYamlContent, err = yamlEditorGuide(
			"",
			false,
			slices.Concat(
				[]byte(clusterYamlCommentHeader),
				[]byte("#\n"),
				[]byte(clusterK8sdYamlRecoveryComment),
				[]byte(yamlHelperCommentFooter),
				[]byte("\n"),
				oldMembersYaml,
			),
			false,
		)
		if err != nil {
			return "", fmt.Errorf("interactive text editor failed, error: %w", err)
		}

		infoYamlPath := path.Join(m.FileSystem.DatabaseDir, "info.yaml")
		infoYamlCommentHeader := fmt.Sprintf("# K8sd info.yaml\n# (%s)\n", infoYamlPath)
		_, err = yamlEditorGuide(
			infoYamlPath,
			true,
			slices.Concat(
				[]byte(infoYamlCommentHeader),
				[]byte("#\n"),
				[]byte(infoYamlRecoveryComment),
				utils.YamlCommentLines(clusterYamlContent),
				[]byte("\n"),
				[]byte(yamlHelperCommentFooter),
			),
			true,
		)
		if err != nil {
			return "", fmt.Errorf("interactive text editor failed, error: %w", err)
		}

		daemonYamlPath := path.Join(m.FileSystem.StateDir, "daemon.yaml")
		daemonYamlCommentHeader := fmt.Sprintf("# K8sd daemon.yaml\n# (%s)\n", daemonYamlPath)
		_, err = yamlEditorGuide(
			daemonYamlPath,
			true,
			slices.Concat(
				[]byte(daemonYamlCommentHeader),
				[]byte("#\n"),
				[]byte(daemonYamlRecoveryComment),
				utils.YamlCommentLines(clusterYamlContent),
				[]byte("\n"),
				[]byte(yamlHelperCommentFooter),
			),
			true,
		)
		if err != nil {
			return "", fmt.Errorf("interactive text editor failed, error: %w", err)
		}
	}

	newMembers := []cluster.DqliteMember{}
	if err = yaml.Unmarshal(clusterYamlContent, &newMembers); err != nil {
		return "", fmt.Errorf("couldn't parse cluster.yaml, error: %w", err)
	}

	// As of 2.0.2, the following microcluster method will:
	// * validate the member changes
	//     * ensure that no members were added or removed
	//     * the member IDs hasn't changed
	//     * there is at least one voter
	//     * the addresses can be parsed
	//     * there are no duplicate addresses
	// * ensure that all cluster members are stopped
	// * create a database backup
	// * reconfigure Raft
	// * address changes based on the new cluster.yaml:
	//     * refresh the local info.yaml and daemon.yaml
	//     * update the trust store addresses
	//     * prepare an sql script that updates the member addresses from the
	//       "core_cluster_members" table, executed when k8sd starts.
	// * rewrite cluster.yaml
	// * create a recovery tarball of the k8sd database dir and store it
	//   in the state dir.
	tarballPath, err := m.RecoverFromQuorumLoss(newMembers)
	if err != nil {
		return "", fmt.Errorf("k8sd recovery failed, error: %w", err)
	}

	return tarballPath, nil
}
