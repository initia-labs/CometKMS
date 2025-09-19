package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:   "init",
	Short: "Create the default CMKMS configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureConfig(&homeDir)
			privDir := filepath.Join(homeDir, "priv")
			if err := os.MkdirAll(privDir, 0o755); err != nil {
				return fmt.Errorf("failed to create priv directory: %w", err)
			}
			dataDir := filepath.Join(homeDir, "data")
			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				return fmt.Errorf("failed to create data directory: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config written to %s\n", confPath)
			return nil
		},
	}

	rootCmd.AddCommand(cmd)
}
