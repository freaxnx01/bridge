package main

import (
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
		{"unknown override falls back to autodetect", map[string]string{"HERDR_ENV": "1", "BRIDGE_LAUNCHER": "bogus"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectBackend(func(k string) string { return tt.env[k] })
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
