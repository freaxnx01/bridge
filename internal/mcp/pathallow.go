package mcp

import (
	"path/filepath"
	"strings"
)

// PathAllowlist is the set of patterns put_file may write to. Each entry is
// one of:
//   - "dir/**" — any path under dir/, at any depth (including dir/ itself)
//   - "*.ext"  — a root-level file (no "/" in the path) matching the glob
//
// Anything else is treated as an exact-path match. A nil or empty
// PathAllowlist allows nothing — callers needing the built-in default use
// DefaultPathAllowlist explicitly.
type PathAllowlist []string

// DefaultPathAllowlist is used when the server is not configured with an
// explicit --put-file-allowlist / BRIDGE_MCP_PUT_FILE_ALLOWLIST.
var DefaultPathAllowlist = PathAllowlist{"docs/**/*.md", "*.md"}

// deniedPrefix is always rejected regardless of PathAllowlist — an MCP
// server must not be able to edit the workflows that gate it (mirrors
// ADR-002 gate 6 in agent-workflow).
const deniedPrefix = ".github"

// Allows reports whether path may be written by put_file.
func (a PathAllowlist) Allows(path string) bool {
	// Reject absolute paths (must be repo-relative)
	if strings.HasPrefix(path, "/") {
		return false
	}
	// Reject paths with .. segments (path traversal) or . segments
	// by checking if the path is equal to its cleaned version
	if path != filepath.Clean(path) {
		return false
	}
	// Additional check for . and .. as path segments
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "." || part == ".." {
			return false
		}
	}
	// Reject .github directory and anything under it
	if path == deniedPrefix || strings.HasPrefix(path, deniedPrefix+"/") {
		return false
	}
	for _, pattern := range a {
		if matchesAllowlistPattern(pattern, path) {
			return true
		}
	}
	return false
}

func matchesAllowlistPattern(pattern, path string) bool {
	// Handle "dir/**" patterns
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		return path == dir || strings.HasPrefix(path, dir+"/")
	}
	// Handle "dir/**/glob" patterns (e.g. "docs/**/*.md")
	if idx := strings.Index(pattern, "/**"); idx != -1 {
		dir := pattern[:idx]
		glob := pattern[idx+3:] // Skip past "/**"
		if glob != "" && path != dir {
			if strings.HasPrefix(path, dir+"/") {
				// If glob starts with /, match against just the filename
				if strings.HasPrefix(glob, "/") {
					filename := filepath.Base(path)
					globPattern := glob[1:] // Remove the leading /
					matched, _ := filepath.Match(globPattern, filename)
					return matched
				}
				// Otherwise match the full rest of the path
				rest := path[len(dir)+1:]
				matched, _ := filepath.Match(glob, rest)
				return matched
			}
		}
		return false
	}
	if strings.Contains(pattern, "/") {
		return pattern == path
	}
	// A root-level glob only matches a path with no directory segment.
	if strings.Contains(path, "/") {
		return false
	}
	matched, _ := filepath.Match(pattern, path)
	return matched
}
