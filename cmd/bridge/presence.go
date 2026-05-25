package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
)

var presenceJSON bool

var presenceCmd = &cobra.Command{
	Use:   "presence [away|back|auto]",
	Short: "Show presence (read-only in Plan A)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runPresence,
}

func init() {
	presenceCmd.Flags().BoolVar(&presenceJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(presenceCmd)
}

func runPresence(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("setting presence is not implemented yet (Plan B); read-only in Plan A")
	}
	p, err := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
	if err != nil {
		return err
	}
	if presenceJSON {
		return emitJSON(cmd.OutOrStdout(), p)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\n", p.Mode)
	if len(p.Overrides) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "overrides:")
		for k, v := range p.Overrides {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", k, v)
		}
	}
	return nil
}
