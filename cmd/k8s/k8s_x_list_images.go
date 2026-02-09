package k8s

import (
	"strings"

	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/k8sd/images"
	"github.com/spf13/cobra"
)

func newListImagesCmd(env cmdutil.ExecutionEnvironment) *cobra.Command {
	cmd := &cobra.Command{
		Hidden:  true,
		Aliases: []string{"list-images"},
		Short:   "List all container images used by this release",
		Long:    "List all container images used by this k8s release, including component images and dependencies. This is an experimental command.",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println(strings.Join(images.Images(), "\n"))
		},
	}
	return cmd
}
