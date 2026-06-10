package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/guyweissman/agentstore/internal/brand"
	"github.com/guyweissman/agentstore/internal/server"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the AgentStore server",
	}
	cmd.AddCommand(newServerStartCmd(), newServerStopCmd())
	return cmd
}

func newServerStartCmd() *cobra.Command {
	var (
		addr    string
		dataDir string
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the server in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, _ := os.UserHomeDir()
				dataDir = filepath.Join(home, brand.GlobalDirName, "server-data")
			}
			srv, err := server.New(dataDir)
			if err != nil {
				return fmt.Errorf("init server: %w", err)
			}
			// Graceful drain on Ctrl-C / SIGTERM.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Fprintln(os.Stderr, "\ndraining...")
				ctx, cancel := context.WithTimeout(context.Background(), server.DrainTimeout)
				defer cancel()
				srv.Shutdown(ctx)
			}()
			return srv.Start(addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "bind address (overrides server.toml; default 127.0.0.1:8080)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "server data directory")
	return cmd
}

func newServerStopCmd() *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Signal the running server to shut down gracefully",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, _ := os.UserHomeDir()
				dataDir = filepath.Join(home, brand.GlobalDirName, "server-data")
			}
			pidBytes, err := os.ReadFile(filepath.Join(dataDir, brand.PIDFile))
			if err != nil {
				return fmt.Errorf("server not running (no pidfile): %w", err)
			}
			pid, err := strconv.Atoi(string(pidBytes))
			if err != nil {
				return fmt.Errorf("invalid pidfile: %w", err)
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("process %d not found: %w", pid, err)
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal %d: %w", pid, err)
			}
			fmt.Printf("sent SIGTERM to pid %d\n", pid)
			return nil
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "server data directory")
	return cmd
}
