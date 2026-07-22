package audit

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Entry is one audited call to a mutating MCP tool.
type Entry struct {
	Time    time.Time
	Forge   string
	Owner   string
	Repo    string
	Tool    string
	Confirm bool
	Outcome string // "success" | "error" | "refused" | "refused_name_mismatch"
}

// Logger appends one JSON object per line to an audit log file.
type Logger struct {
	slog *slog.Logger
}

// Open opens (creating if absent) the audit log at path in append mode,
// creating any missing parent directories. Callers may call Open on the same
// path more than once (e.g. across process restarts); each returned Logger
// appends independently.
func Open(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create audit log directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open audit log %s: %w", path, err)
	}
	return &Logger{slog: slog.New(slog.NewJSONHandler(f, nil))}, nil
}

// Log appends e as one JSON line. A zero e.Time is stamped with time.Now().
func (l *Logger) Log(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.slog.Info("audit",
		"time", e.Time,
		"forge", e.Forge,
		"owner", e.Owner,
		"repo", e.Repo,
		"tool", e.Tool,
		"confirm", e.Confirm,
		"outcome", e.Outcome,
	)
}
