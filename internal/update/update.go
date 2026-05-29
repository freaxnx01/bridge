// Package update provides a TTL-gated "newer version available" hint
// against GitHub releases (issue #92 / gap G9). On-disk-first: the hint
// itself is rendered from a JSON cache so `bridge --version` never makes
// a network call. MaybeRefresh, invoked from PersistentPreRunE on real
// commands, refreshes the cache when stale.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	Owner = "freaxnx01"
	Repo  = "bridge"
	TTL   = 24 * time.Hour

	// FetchTimeout bounds a single /releases/latest call so a slow forge
	// can't stall a foreground command.
	FetchTimeout = 2 * time.Second
)

// disableEnv is the opt-out env var. Set to any non-empty value to skip
// the check entirely (useful in tests / air-gapped setups).
const disableEnv = "BRIDGE_NO_VERSION_CHECK"

type cacheFile struct {
	Tag       string    `json:"tag"`
	FetchedAt time.Time `json:"fetched_at"`
}

// CachePath returns the on-disk path of the release-check cache file.
// Honors BRIDGE_CACHE, then XDG_CACHE_HOME/bridge, then ~/.cache/bridge.
func CachePath() string {
	return filepath.Join(cacheDir(), "release-check.json")
}

func cacheDir() string {
	if x := os.Getenv("BRIDGE_CACHE"); x != "" {
		return x
	}
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "bridge")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "bridge")
}

// Read returns the cached release tag and its fetch timestamp, or
// ("", zero, err) when the cache is missing/unreadable.
func Read() (string, time.Time, error) {
	raw, err := os.ReadFile(CachePath())
	if err != nil {
		return "", time.Time{}, err
	}
	var c cacheFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", time.Time{}, err
	}
	return c.Tag, c.FetchedAt, nil
}

// Write atomically persists the cache. The directory is created if absent.
func Write(tag string, fetchedAt time.Time) error {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(cacheFile{Tag: tag, FetchedAt: fetchedAt}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".release-check-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(buf); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, CachePath())
}

// Fetch returns the latest release tag for owner/repo via the GitHub API.
// apiBase lets tests substitute an httptest server; pass "" for the
// default https://api.github.com.
func Fetch(ctx context.Context, apiBase, owner, repo string) (string, error) {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(apiBase, "/"), owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bridge-update-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain to allow connection reuse.
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("update: GitHub returned %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", errors.New("update: empty tag_name in response")
	}
	return body.TagName, nil
}

// MaybeRefresh refreshes the cache if it's missing, older than ttl, or
// currently holds a tag <= current (so a fresh release shows up promptly
// once published). No-op when BRIDGE_NO_VERSION_CHECK is set or current
// is "dev" / empty. apiBase "" means real api.github.com.
func MaybeRefresh(ctx context.Context, current, apiBase string) {
	if os.Getenv(disableEnv) != "" {
		return
	}
	if current == "" || current == "dev" {
		return
	}
	if _, fetched, err := Read(); err == nil && time.Since(fetched) < TTL {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	latest, err := Fetch(fetchCtx, apiBase, Owner, Repo)
	if err != nil {
		return
	}
	_ = Write(latest, time.Now().UTC())
}

// Hint returns a one-line "newer version available" hint when the cache
// holds a tag strictly newer than current. Returns "" otherwise — when
// there's no cache, no usable current version, or no newer release.
func Hint(current string) string {
	if current == "" || current == "dev" {
		return ""
	}
	tag, _, err := Read()
	if err != nil || tag == "" {
		return ""
	}
	if !Newer(current, tag) {
		return ""
	}
	return fmt.Sprintf("  → newer version available: %s  (https://github.com/%s/%s/releases)", tag, Owner, Repo)
}

// Newer reports whether latest is strictly newer than current.
// Returns false on parse failure (so dirty-build or pre-release current
// values silently suppress the hint).
func Newer(current, latest string) bool {
	cMaj, cMin, cPat, ok := parseSemver(current)
	if !ok {
		return false
	}
	lMaj, lMin, lPat, ok := parseSemver(latest)
	if !ok {
		return false
	}
	switch {
	case lMaj != cMaj:
		return lMaj > cMaj
	case lMin != cMin:
		return lMin > cMin
	default:
		return lPat > cPat
	}
}

// parseSemver pulls the leading X.Y.Z out of strings like "v2.2.0",
// "2.2.0-1-gabcdef", or "v2.2.0-dirty". Returns ok=false on garbage.
func parseSemver(s string) (int, int, int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// Cut at the first non-version separator.
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return maj, min, pat, true
}
