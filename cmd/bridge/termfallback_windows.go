//go:build windows

package main

import "io"

// maybeTermFallback is a no-op on Windows: the launcher uses Windows Terminal
// (wt.exe), which has no terminfo concept, so there is nothing to fall back
// from. Mirrors the unix signature so preflight.go compiles on both. See #104.
func maybeTermFallback(_ io.Writer, argv []string) []string {
	return argv
}
