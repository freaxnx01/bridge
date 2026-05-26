package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
)

func setPresence(cmd *cobra.Command, mode string) error {
	switch mode {
	case "away", "back", "auto":
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown presence mode %q (want away|back|auto)\n", mode)
		os.Exit(2)
	}
	p, _ := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
	p.Mode = mode
	p.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(filepath.Join(cacheRoot(), "presence.json"), b)
}
