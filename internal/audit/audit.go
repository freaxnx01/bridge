package audit

import (
	"context"
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
	Outcome string // "success" | "error" | "refused" | "refused_name_mismatch" | "partial"
}

// Logger appends one JSON object per line to an audit log file.
type Logger struct {
	handler slog.Handler
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
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
				return slog.Attr{}
			}
			return a
		},
	})
	return &Logger{handler: handler}, nil
}

// Log appends e as one JSON line. A zero e.Time is stamped with time.Now().
func (l *Logger) Log(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	r := slog.NewRecord(e.Time, slog.LevelInfo, "audit", 0)
	r.AddAttrs(
		slog.String("forge", e.Forge),
		slog.String("owner", e.Owner),
		slog.String("repo", e.Repo),
		slog.String("tool", e.Tool),
		slog.Bool("confirm", e.Confirm),
		slog.String("outcome", e.Outcome),
	)
	_ = l.handler.Handle(context.Background(), r) // best-effort write
}
