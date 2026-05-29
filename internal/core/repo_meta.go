package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// RepoMeta is per-repo metadata cached from forge APIs by `bridge list -r`.
// Keyed in repo-meta.json by relative path under the repos root.
type RepoMeta struct {
	Description   string   `json:"description,omitempty"`
	Topics        []string `json:"topics,omitempty"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	RemoteURL     string   `json:"remote_url,omitempty"`
}

// LoadRepoMeta reads the on-disk metadata cache. Missing file → empty map, no error.
// Tolerates unknown extra fields (e.g. fetched_at) per encoding/json defaults.
func LoadRepoMeta(path string) (map[string]RepoMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]RepoMeta{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return map[string]RepoMeta{}, nil
	}
	out := map[string]RepoMeta{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MergeRepoMeta fills empty Topics / Desc / DefaultBranch / RemoteURL fields on
// each repo from meta keyed by the repo's path-relative-to-its-base. With
// multiple roots configured (gap G3 / #86) each repo's path is matched
// against every root and the first one yielding a "non-escaping" relative
// path wins. Existing non-empty fields are preserved. Missing cache entries
// leave fields untouched. Pure function — testable without disk.
func MergeRepoMeta(repos []Repo, reposRoots []string, meta map[string]RepoMeta) []Repo {
	if len(meta) == 0 {
		return repos
	}
	out := make([]Repo, len(repos))
	for i, r := range repos {
		rel := bestRelUnder(reposRoots, r.Path)
		m, ok := meta[rel]
		if !ok {
			out[i] = r
			continue
		}
		if r.Desc == "" {
			r.Desc = m.Description
		}
		if len(r.Topics) == 0 && len(m.Topics) > 0 {
			r.Topics = m.Topics
		}
		if r.DefaultBranch == "" {
			r.DefaultBranch = m.DefaultBranch
		}
		if r.RemoteURL == "" {
			r.RemoteURL = m.RemoteURL
		}
		out[i] = r
	}
	return out
}

func relUnder(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(strings.TrimPrefix(rel, "./"))
}

// bestRelUnder returns the shortest non-escaping rel-path of p under any of
// the bases. An "escaping" rel starts with "../" (the base is unrelated to
// p). Falls back to p when no base matches.
func bestRelUnder(bases []string, p string) string {
	best := p
	for _, b := range bases {
		r := relUnder(b, p)
		if strings.HasPrefix(r, "../") || r == ".." {
			continue
		}
		if best == p || len(r) < len(best) {
			best = r
		}
	}
	return best
}
