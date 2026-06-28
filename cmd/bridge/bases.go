package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
)

// baseFlag holds the values of repeated `-B`/`--base` flags. Populated by
// rootCmd's PersistentFlags. Reset between subprocess invocations because
// every test spawns a fresh binary.
var baseFlag []string

// reposRoots returns the ordered list of base directories to walk for repo
// discovery. Precedence (the first non-empty source wins entirely):
//
//  1. `-B`/`--base` (one or more, comma-split per cobra StringSlice)
//  2. `BRIDGE_BASE` env (colon-separated)
//  3. `BRIDGE_REPOS_ROOT` env (legacy single value)
//  4. `$XDG_CONFIG_HOME/bridge/base` (one path per line)
//  5. `$HOME/repos` (default)
//
// Result is de-duplicated by absolute path and the first occurrence wins.
// Empty entries are dropped.
func reposRoots() []string {
	switch {
	case len(baseFlag) > 0:
		return dedupeAbs(splitMany(baseFlag, ","))
	case os.Getenv("BRIDGE_BASE") != "":
		return dedupeAbs(strings.Split(os.Getenv("BRIDGE_BASE"), string(os.PathListSeparator)))
	case os.Getenv("BRIDGE_REPOS_ROOT") != "":
		return dedupeAbs([]string{os.Getenv("BRIDGE_REPOS_ROOT")})
	}
	if cfg, ok := readBaseFile(); ok && len(cfg) > 0 {
		return dedupeAbs(cfg)
	}
	home, _ := os.UserHomeDir()
	return dedupeAbs([]string{filepath.Join(home, "repos")})
}

// reposRoot returns the first (primary) base. Used by call sites that
// need a single root — clone target dir choice, the TUI dashboard until
// it learns to span multiple bases. Stays a single-valued accessor so
// the change to reposRoots() doesn't ripple everywhere.
func reposRoot() string {
	roots := reposRoots()
	if len(roots) == 0 {
		return ""
	}
	return roots[0]
}

// discoverAllRoots fans DiscoverRepos out over reposRoots() and dedupes
// by absolute repo path. Missing bases emit a stderr warning but don't
// abort discovery (the user may have a stale config file entry).
func discoverAllRoots() ([]core.Repo, error) {
	roots := reposRoots()
	var all []core.Repo
	seen := map[string]bool{}
	for _, root := range roots {
		if !dirExists(root) {
			if len(roots) > 1 {
				// Only warn when there's more than one configured base:
				// the single-base case (default ~/repos on a
				// fresh checkout) shouldn't pester the user.
				fmt.Fprintf(os.Stderr, "warning: base %q does not exist\n", root)
			}
			continue
		}
		repos, err := core.DiscoverRepos(root)
		if err != nil {
			return nil, fmt.Errorf("discover %s: %w", root, err)
		}
		for _, r := range repos {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			all = append(all, r)
		}
	}
	return all, nil
}

func splitMany(in []string, sep string) []string {
	var out []string
	for _, s := range in {
		for _, p := range strings.Split(s, sep) {
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func dedupeAbs(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// readBaseFile reads the optional config file at
// `$XDG_CONFIG_HOME/bridge/base` (or `$HOME/.config/bridge/base`).
// Lines starting with `#` and blank lines are ignored.
func readBaseFile() ([]string, bool) {
	path := baseFilePath()
	if path == "" {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, true
}

func baseFilePath() string {
	if v := os.Getenv("BRIDGE_BASE_FILE"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "bridge", "base")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "bridge", "base")
}
