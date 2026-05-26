package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
)

var (
	statusJSON bool
	statusSlim bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Composed summary + per-slot/session detail table",
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "machine-readable output")
	statusCmd.Flags().BoolVar(&statusSlim, "slim", false, "summary lines only (drop the detail table)")
	rootCmd.AddCommand(statusCmd)
}

type statusRow struct {
	Slot     string `json:"slot"`
	Kind     string `json:"kind"`
	Repo     string `json:"repo"`
	Agent    string `json:"agent,omitempty"`
	Age      string `json:"age"`
	State    string `json:"state,omitempty"`
	TmuxName string `json:"tmux_name"`
	PID      int    `json:"pid,omitempty"`
}

// statusOut keeps the legacy flat top-level shape (sessions/presence/sync/version)
// for backward compatibility with existing --json consumers, with the new rows
// field added alongside.
type statusOut struct {
	Sessions int    `json:"sessions"`
	Presence string `json:"presence"`
	Sync     struct {
		Unpushed int `json:"unpushed"`
	} `json:"sync"`
	Version string      `json:"version"`
	Rows    []statusRow `json:"rows,omitempty"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	var out statusOut
	out.Version = versionString()

	sessions, _ := loadSessions()
	out.Sessions = len(sessions)

	pr, _ := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
	out.Presence = pr.Mode

	if b, err := store.ReadFile(filepath.Join(cacheRoot(), "sync.json")); err == nil && len(b) > 0 {
		var sy SyncState
		if err := json.Unmarshal(b, &sy); err == nil {
			out.Sync.Unpushed = len(sy.Unpushed)
		}
	}

	if !statusSlim {
		slots, _ := core.LoadSlots(filepath.Join(cacheRoot(), "slots.json"))
		out.Rows = composeStatusRows(slots, sessions, time.Now())
	}

	if statusJSON {
		return emitJSON(cmd.OutOrStdout(), out)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "sessions:  %d\n", out.Sessions)
	fmt.Fprintf(w, "presence:  %s\n", out.Presence)
	fmt.Fprintf(w, "unpushed:  %d\n", out.Sync.Unpushed)
	fmt.Fprintf(w, "version:   %s\n", out.Version)

	if statusSlim {
		return nil
	}
	writeStatusTable(w, out.Rows)
	return nil
}

// composeStatusRows joins the slot registry with live tmux sessions: every
// slot becomes a row (kind=slot), with State+TmuxName populated from the
// matching live session if one exists; otherwise both render as "—" (stale).
//
// Untagged tmux sessions (running on this host but not in the slot registry)
// are NOT surfaced here — the LiveSessions enumeration is unfiltered, so it
// would otherwise leak unrelated shells / admin sessions / etc. into the
// bridge status table. Adding them back with a pane-command filter (e.g.
// keep only sessions whose pane runs `claude`) is a separate enhancement.
//
// Pure function — testable without disk or tmux access.
func composeStatusRows(slots []core.Slot, sessions []core.Session, now time.Time) []statusRow {
	sessionsByID := map[string]core.Session{}
	for _, s := range sessions {
		sessionsByID[s.SlotID] = s
	}
	var rows []statusRow
	for _, slot := range slots {
		repo := slot.Repo
		if slot.Worktree != "" {
			repo = repo + " [" + slot.Worktree + "]"
		}
		row := statusRow{
			Slot:     slot.ID,
			Kind:     "slot",
			Repo:     repo,
			Agent:    slot.Agent,
			Age:      humanDuration(now.Sub(slot.Created)),
			State:    "—",
			TmuxName: "—",
		}
		if live, ok := sessionsByID[slot.ID]; ok {
			row.State = live.State
			row.TmuxName = live.TmuxName
			row.PID = live.PID
		}
		rows = append(rows, row)
	}
	return rows
}

func writeStatusTable(w io.Writer, rows []statusRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "\nno active slots or sessions")
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-20s %-6s %-30s %-10s %-10s %-10s %s\n", "slot", "kind", "repo", "agent", "age", "state", "tmux")
	for _, r := range rows {
		agent := r.Agent
		if agent == "" {
			agent = "—"
		}
		fmt.Fprintf(w, "%-20s %-6s %-30s %-10s %-10s %-10s %s\n",
			r.Slot, r.Kind, truncate(r.Repo, 30), truncate(agent, 10), r.Age, r.State, r.TmuxName)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
