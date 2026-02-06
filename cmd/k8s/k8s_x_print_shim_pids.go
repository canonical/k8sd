package k8s

import (
	"github.com/canonical/k8sd/pkg/utils/shims"
	"github.com/spf13/cobra"
)

var xPrintShimPidsCmd = &cobra.Command{
	Use:    "x-print-shim-pids",
	Short:  "Print containerd shim and pause process PIDs",
	Long:   "Print a list of process IDs (PIDs) for containerd shim and pause processes. This is an experimental command.",
	Hidden: true,
	Args:   cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		pids, err := shims.RunningContainerdShimPIDs(cmd.Context())
		if err != nil {
			panic(err)
		}
		for _, pid := range pids {
			cmd.Println(pid)
		}
	},
}
