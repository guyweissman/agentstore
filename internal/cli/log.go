package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/store"
	"github.com/guyweissman/agentstore/internal/workspace"
)

func newLogCmd() *cobra.Command {
	var (
		limit    int
		author   string
		since    string
		to       string
		cursor   int64
		toCursor int64
		reverse  bool
		asJSON   bool
	)

	cmd := &cobra.Command{
		Use:   "log [<path>]",
		Short: "Show commit history",
		Args:  cobra.MaximumNArgs(1),
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

			f := store.LogFilter{
				Author:   author,
				Cursor:   cursor,
				ToCursor: toCursor,
				Limit:    limit,
				Reverse:  reverse,
			}
			if len(args) == 1 {
				sp, err := workspace.StorePath(root, args[0])
				if err != nil {
					return err
				}
				f.Path = sp
			}
			if since != "" {
				t, err := time.Parse(time.RFC3339, since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				f.Since = t.UnixMilli()
			}
			if to != "" {
				t, err := time.Parse(time.RFC3339, to)
				if err != nil {
					return fmt.Errorf("--to: %w", err)
				}
				f.To = t.UnixMilli()
			}

			commits, err := s.LogCommits(f)
			if err != nil {
				return err
			}

			if asJSON {
				return printLogJSON(os.Stdout, commits)
			}
			printLogHuman(commits)
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "number", "n", 0, "limit to the most recent N commits")
	cmd.Flags().StringVar(&author, "author", "", "filter by author principal ID")
	cmd.Flags().StringVar(&since, "since", "", "commits at or after this ISO-8601 date")
	cmd.Flags().StringVar(&to, "to", "", "commits at or before this ISO-8601 date")
	cmd.Flags().Int64Var(&cursor, "cursor", 0, "commits after this seq cursor")
	cmd.Flags().Int64Var(&toCursor, "to-cursor", 0, "commits up to this seq cursor")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "show oldest first")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable JSON output")
	return cmd
}

func printLogHuman(commits []*store.Commit) {
	for _, c := range commits {
		short := c.ID
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Printf("commit %s (seq %d)\n", short, c.Seq)
		fmt.Printf("Author: %s\n", c.AuthorID)
		fmt.Printf("Date:   %s\n\n", time.UnixMilli(c.CreatedAt).UTC().Format(time.RFC3339))
		fmt.Printf("    %s\n\n", c.Message)
		for _, f := range c.Files {
			fmt.Printf("    %s  %s\n", changeSymbol(f.ChangeType), f.Path)
		}
		if len(c.Files) > 0 {
			fmt.Println()
		}
	}
}

func printLogJSON(w io.Writer, commits []*store.Commit) error {
	type fileJSON struct {
		Path       string `json:"path"`
		ObjectHash string `json:"object_hash,omitempty"`
		Size       int64  `json:"size,omitempty"`
		ChangeType string `json:"change_type"`
	}
	type commitJSON struct {
		ID        string     `json:"id"`
		Seq       int64      `json:"seq"`
		Message   string     `json:"message"`
		AuthorID  string     `json:"author_id"`
		CreatedAt int64      `json:"created_at"`
		Parents   []string   `json:"parents"`
		Files     []fileJSON `json:"files"`
	}
	enc := json.NewEncoder(w)
	for _, c := range commits {
		files := make([]fileJSON, len(c.Files))
		for i, f := range c.Files {
			files[i] = fileJSON{Path: f.Path, ObjectHash: f.ObjectHash, Size: f.Size, ChangeType: f.ChangeType}
		}
		parents := c.Parents
		if parents == nil {
			parents = []string{}
		}
		if err := enc.Encode(commitJSON{
			ID: c.ID, Seq: c.Seq, Message: c.Message,
			AuthorID: c.AuthorID, CreatedAt: c.CreatedAt,
			Parents: parents, Files: files,
		}); err != nil {
			return err
		}
	}
	return nil
}
