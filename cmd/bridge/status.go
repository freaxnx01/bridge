package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
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
		// Pane commands are used to filter untagged tmux sessions to known
		// agents only. Best-effort: errors leave panes=nil, which makes
		// composeStatusRows skip the untagged-tmux block entirely.
		panes, _ := core.LivePaneCommands()
		out.Rows = composeStatusRows(slots, sessions, panes, time.Now())
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

// composeStatusRows joins the slot registry with live tmux sessions:
//   - Every slot becomes a row (kind=slot). State+TmuxName come from a
//     matching live session if one exists; otherwise both render as "—".
//   - Live tmux sessions NOT in the slot registry are surfaced as kind=tmux
//     rows only when at least one of their panes runs a known agent command
//     (see core.KnownAgentCommands). This filter prevents unrelated shells /
//     admin sessions from leaking into bridge status.
//
// paneCommands maps session_name → list of pane_current_command values for
// that session. Pass nil to disable untagged-tmux surfacing entirely.
//
// Pure function — testable without disk or tmux access.
func composeStatusRows(slots []core.Slot, sessions []core.Session, paneCommands map[string][]string, now time.Time) []statusRow {
	sessionsByID := map[string]core.Session{}
	for _, s := range sessions {
		sessionsByID[s.SlotID] = s
	}
	covered := map[string]bool{}
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
			covered[slot.ID] = true
		}
		rows = append(rows, row)
	}
	if paneCommands == nil {
		return rows
	}
	var extras []core.Session
	for _, s := range sessions {
		if covered[s.SlotID] {
			continue
		}
		if !core.SessionRunsKnownAgent(paneCommands[s.SlotID]) {
			continue
		}
		extras = append(extras, s)
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i].SlotID < extras[j].SlotID })
	for _, s := range extras {
		rows = append(rows, statusRow{
			Slot:     s.SlotID,
			Kind:     "tmux",
			Repo:     "—",
			Age:      humanDuration(s.Age),
			State:    s.State,
			TmuxName: s.TmuxName,
			PID:      s.PID,
		})
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
