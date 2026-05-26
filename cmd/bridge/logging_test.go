package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestInstallLoggerSilentByDefault(t *testing.T) {
	var buf bytes.Buffer
	h := installLogger(&buf, 0, "")
	slog.SetDefault(slog.New(h))
	slog.Info("hi")
	if buf.Len() != 0 {
		t.Errorf("expected silent, got %q", buf.String())
	}
}

func TestInstallLoggerVerboseEmitsInfo(t *testing.T) {
	var buf bytes.Buffer
	h := installLogger(&buf, 1, "")
	slog.SetDefault(slog.New(h))
	slog.Info("hi")
	if !strings.Contains(buf.String(), "hi") {
		t.Errorf("expected hi in %q", buf.String())
	}
}

func TestInstallLoggerVeryVerboseEmitsDebug(t *testing.T) {
	var buf bytes.Buffer
	h := installLogger(&buf, 2, "")
	slog.SetDefault(slog.New(h))
	slog.Debug("dbg")
	if !strings.Contains(buf.String(), "dbg") {
		t.Errorf("expected dbg in %q", buf.String())
	}
}
