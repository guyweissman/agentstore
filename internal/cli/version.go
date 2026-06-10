package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/buildinfo"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s\n", brand.AppName, buildinfo.Version())
			if c := buildinfo.Commit(); c != "" {
				fmt.Fprintf(out, "commit: %s\n", c)
			}
			if d := buildinfo.Date(); d != "" {
				fmt.Fprintf(out, "built:  %s\n", d)
			}
			return nil
		},
	}
}
