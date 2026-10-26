package navdata

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// IssueCacheTTL is how long a per-repo issue cache is served without a refetch.
const IssueCacheTTL = 10 * time.Minute

// ErrNoForgeClient is what a fetcher returns when no client (no token) is
// configured for the forge. Like any fetch error it is never cached, so a
// missing token cannot hide a repo's issues behind a fresh empty cache.
var ErrNoForgeClient = errors.New("no forge client configured")

// IssueSource configures issue loading. CacheDir "" disables the cache; Fetch
// nil disables live fetching (cache only).
type IssueSource struct {
	CacheDir string
	Fetch    func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error)
}

// IssueCacheFile is the per-repo cache path, or "" when caching is disabled.
func IssueCacheFile(dir, forgeName, owner, repo string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, forgeName+"_"+owner+"_"+repo+".json")
}

// IssuesCached returns a repo's open issues: a fresh cache wins; otherwise it
// fetches and rewrites the cache. A fetch error — ErrNoForgeClient included —
// (or no fetcher) falls back to the last cached slice and writes nothing.
func IssuesCached(ctx context.Context, src IssueSource, forgeName, owner, repo string) []forge.Issue {
	cacheFile := IssueCacheFile(src.CacheDir, forgeName, owner, repo)
	var cached forge.IssueCache
	if cacheFile != "" {
		cached, _ = forge.ReadIssueCache(cacheFile)
		if !cached.UpdatedAt.IsZero() && !cached.IsStale(IssueCacheTTL) {
			return cached.Issues
		}
	}
	if src.Fetch == nil {
		return cached.Issues
	}
	issues, err := src.Fetch(ctx, forgeName, owner, repo)
	if err != nil {
		return cached.Issues
	}
	if cacheFile != "" {
		_ = forge.WriteIssueCache(cacheFile, forge.IssueCache{UpdatedAt: time.Now(), Issues: issues})
	}
	return issues
}
