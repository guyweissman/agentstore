package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/skill"
)

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Export the agent skill that teaches an AI assistant to use " + brand.AppName,
	}
	cmd.AddCommand(newSkillExportCmd())
	return cmd
}

func newSkillExportCmd() *cobra.Command {
	var stdout bool
	cmd := &cobra.Command{
		Use:   "export [<dir>]",
		Short: "Write the skill (SKILL.md + reference) to a directory for any agent runtime",
		Long: "Export the bundled agent skill as portable markdown. Point your AI\n" +
			"assistant's skills directory at the result (Claude Code, Codex, and other\n" +
			"runtimes all read SKILL.md). Use --stdout to print SKILL.md without writing files.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdout {
				md, err := skill.SkillMarkdown()
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(md)
				return err
			}

			dir := brand.AppName + "-skill"
			if len(args) == 1 {
				dir = args[0]
			}
			if err := skill.Export(dir); err != nil {
				return err
			}
			fmt.Printf("Wrote skill to %s/ (SKILL.md + reference/)\n", dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&stdout, "stdout", false, "print SKILL.md to stdout instead of writing files")
	return cmd
}
