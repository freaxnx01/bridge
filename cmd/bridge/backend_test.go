package main

import (
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/herdr"
)

func TestSelectBackend_ResolvesFromTheEnvironment(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantHerdr bool
	}{
		{"nothing set", map[string]string{}, false},
		{"inside herdr", map[string]string{"HERDR_ENV": "1"}, true},
		{"inside herdr but opted out", map[string]string{"HERDR_ENV": "1", "BRIDGE_LAUNCHER": "tmux"}, false},
		{"opted in from outside", map[string]string{"BRIDGE_LAUNCHER": "herdr"}, true},
		{"HERDR_ENV set to something else", map[string]string{"HERDR_ENV": "0"}, false},
		{"unknown override still autodetects, but warns", map[string]string{"HERDR_ENV": "1", "BRIDGE_LAUNCHER": "bogus"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var warn strings.Builder
			got := selectBackend(func(k string) string { return tt.env[k] }, &warn)
			if got == nil {
				t.Fatal("selectBackend returned nil; nav would have no backend")
			}
			_, isHerdr := got.(*herdr.Client)
			if isHerdr != tt.wantHerdr {
				t.Errorf("herdr backend = %v, want %v", isHerdr, tt.wantHerdr)
			}
		})
	}
}

func TestSelectBackend_UnrecognizedLauncherValue_WarnsInsteadOfSilentlyIgnoring(t *testing.T) {
	// A typo in an explicitly-set BRIDGE_LAUNCHER must not route the user to a
	// backend they did not ask for without saying so.
	var warn strings.Builder
	selectBackend(func(k string) string {
		return map[string]string{"BRIDGE_LAUNCHER": "tmxu"}[k]
	}, &warn)
	if !strings.Contains(warn.String(), "tmxu") {
		t.Errorf("warning = %q, want it to name the unrecognized value", warn.String())
	}
}

func TestSelectBackend_RecognizedOrUnsetValue_WarnsAboutNothing(t *testing.T) {
	for _, v := range []string{"", "tmux", "herdr"} {
		t.Run("BRIDGE_LAUNCHER="+v, func(t *testing.T) {
			var warn strings.Builder
			selectBackend(func(k string) string {
				return map[string]string{"BRIDGE_LAUNCHER": v}[k]
			}, &warn)
			if warn.String() != "" {
				t.Errorf("unexpected warning: %q", warn.String())
			}
		})
	}
}
