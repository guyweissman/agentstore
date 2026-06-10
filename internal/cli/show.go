package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/store"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <commit>",
		Short: "Show a commit and its changes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := store.FindRoot(cwd)
			if err != nil {
				return err
			}
			s, err := store.Open(root)
			if err != nil {
				return err
			}
			defer s.Close()
			return RunShow(os.Stdout, s, args[0])
		},
	}
}

// RunShow prints a single commit to w.
func RunShow(w interface{ WriteString(string) (int, error) }, s *store.Store, commitID string) error {
	c, err := s.GetCommit(commitID)
	if err != nil {
		return err
	}

	short := c.ID
	if len(short) > 8 {
		short = short[:8]
	}
	write := func(format string, args ...any) {
		w.WriteString(fmt.Sprintf(format, args...))
	}

	write("commit %s (seq %d)\n", short, c.Seq)
	write("Author: %s\n", c.AuthorID)
	write("Date:   %s\n", time.UnixMilli(c.CreatedAt).UTC().Format(time.RFC3339))
	if len(c.Parents) > 0 {
		write("Merge:  ")
		for i, p := range c.Parents {
			if i > 0 {
				write(" ")
			}
			if len(p) > 8 {
				write("%s", p[:8])
			} else {
				write("%s", p)
			}
		}
		write("\n")
	}
	write("\n    %s\n\n", c.Message)

	for _, f := range c.Files {
		write("%s  %s\n", changeSymbol(f.ChangeType), f.Path)
		if f.ObjectHash != "" {
			data, err := s.Objects.ReadObject(f.ObjectHash)
			if err == nil {
				// Show first 512 bytes as a preview.
				preview := string(data)
				if len(preview) > 512 {
					preview = preview[:512] + "..."
				}
				for _, line := range splitLines(preview) {
					write("+%s", line)
				}
			}
		}
	}
	return nil
}

func changeSymbol(changeType string) string {
	switch changeType {
	case "added":
		return "A"
	case "deleted":
		return "D"
	default:
		return "M"
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
