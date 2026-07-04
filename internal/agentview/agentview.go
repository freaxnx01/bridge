// Package agentview wraps `claude agents --json`, Claude Code's local listing of
// live agent sessions, into typed values for bridge's nav TUI and WebUI. It is a
// read-only reporter: it never attaches to, steers, or kills a session.
package agentview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"
)

// Session is one live Claude Code session reported by `claude agents --json`.
type Session struct {
	PID       int       `json:"pid"`
	CWD       string    `json:"cwd"`
	Kind      string    `json:"kind"` // "interactive" | "background"
	SessionID string    `json:"sessionId"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // "busy" | "idle" | ...
	StartedAt time.Time `json:"startedAt"`
}

// Runner runs an external command and returns its stdout. The consumer defines it
// so tests inject a fake without a real `claude` binary.
type Runner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the production Runner: it shells out via os/exec.
type ExecRunner struct{}

// Output runs name+args and returns stdout.
func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ErrUnavailable means the claude CLI is absent or `claude agents` failed. Callers
// render an "unavailable" state rather than surfacing a hard error.
var ErrUnavailable = errors.New("claude agent view unavailable")

// dto mirrors the raw JSON entry; startedAt is epoch milliseconds.
type dto struct {
	PID       int    `json:"pid"`
	CWD       string `json:"cwd"`
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"`
}

// List returns the live Claude sessions from `claude agents --json`, sorted
// busy-first then by name for a stable display order. An empty array is a valid
// zero-session result. A run failure (missing binary / non-zero exit) is wrapped as
// ErrUnavailable; malformed JSON returns a distinct parse error.
func List(ctx context.Context, run Runner) ([]Session, error) {
	out, err := run.Output(ctx, "claude", "agents", "--json")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var raw []dto
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse claude agents json: %w", err)
	}
	sessions := make([]Session, 0, len(raw))
	for _, d := range raw {
		sessions = append(sessions, Session{
			PID:       d.PID,
			CWD:       d.CWD,
			Kind:      d.Kind,
			SessionID: d.SessionID,
			Name:      d.Name,
			Status:    d.Status,
			StartedAt: time.UnixMilli(d.StartedAt),
		})
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		bi, bj := sessions[i].Status == "busy", sessions[j].Status == "busy"
		if bi != bj {
			return bi // busy first
		}
		return sessions[i].Name < sessions[j].Name
	})
	return sessions, nil
}
