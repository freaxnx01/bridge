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
// each repo from meta keyed by the repo's path-relative-to-reposRoot. Existing
// non-empty fields are preserved. Missing cache entries leave fields untouched.
// Pure function — testable without disk.
func MergeRepoMeta(repos []Repo, reposRoot string, meta map[string]RepoMeta) []Repo {
	if len(meta) == 0 {
		return repos
	}
	out := make([]Repo, len(repos))
	for i, r := range repos {
		rel := relUnder(reposRoot, r.Path)
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
