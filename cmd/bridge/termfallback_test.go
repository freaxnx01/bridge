//go:build !windows

package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// withTermResolver swaps the package-level termResolver for the duration of a
// test and restores it afterward.
func withTermResolver(t *testing.T, fn func(string) bool) {
	t.Helper()
	prev := termResolver
	termResolver = fn
	t.Cleanup(func() { termResolver = prev })
}

func TestMaybeTermFallback(t *testing.T) {
	argv := []string{"tmux", "new-session", "-A", "-s", "repo", "-c", "/p", "claude"}

	t.Run("term unset: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		got := maybeTermFallback(&errBuf, argv)
		if !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged %v", got, argv)
		}
		if errBuf.Len() != 0 {
			t.Errorf("unexpected notice: %q", errBuf.String())
		}
	})

	t.Run("term already xterm-256color: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-256color")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("disable var set: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		t.Setenv("BRIDGE_NO_TERM_FALLBACK", "1")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("term resolves: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		t.Setenv("BRIDGE_NO_TERM_FALLBACK", "")
		withTermResolver(t, func(string) bool { return true })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("term unresolved: prefix env and notice", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		t.Setenv("BRIDGE_NO_TERM_FALLBACK", "")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		got := maybeTermFallback(&errBuf, argv)
		want := append([]string{"env", "TERM=xterm-256color"}, argv...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		notice := errBuf.String()
		if !strings.Contains(notice, "xterm-kitty") ||
			!strings.Contains(notice, "TERM=xterm-256color") ||
			!strings.Contains(notice, "BRIDGE_NO_TERM_FALLBACK") {
			t.Errorf("notice missing expected content: %q", notice)
		}
	})

	t.Run("empty argv: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, nil); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}
