package main

import (
	"context"
	"io"
	"log/slog"

	"gopkg.in/natefinch/lumberjack.v2"
)

// installLogger returns an slog.Handler configured for the given verbosity:
//
//	0 → silent (discard).
//	1 → INFO to stderrWriter (human text).
//	2 → DEBUG to stderrWriter (human text).
//
// If logFile != "", a JSON handler is teed to a rotating logfile (10MB / 3 keep)
// at INFO level regardless of stderr verbosity.
func installLogger(stderrWriter io.Writer, verbose int, logFile string) slog.Handler {
	stderrLevel := slog.LevelError + 100
	switch verbose {
	case 1:
		stderrLevel = slog.LevelInfo
	case 2:
		stderrLevel = slog.LevelDebug
	}
	stderrHandler := slog.NewTextHandler(stderrWriter, &slog.HandlerOptions{Level: stderrLevel})

	if logFile == "" {
		return stderrHandler
	}
	lj := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    10,
		MaxBackups: 3,
	}
	fileHandler := slog.NewJSONHandler(lj, &slog.HandlerOptions{Level: slog.LevelInfo})
	return multiHandler{stderrHandler, fileHandler}
}

type multiHandler []slog.Handler

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range m {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m multiHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(as)
	}
	return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}
	return out
}
