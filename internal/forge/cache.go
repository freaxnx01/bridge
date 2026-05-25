package forge

import (
	"encoding/json"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

type IssueCache struct {
	UpdatedAt time.Time `json:"updated_at"`
	Issues    []Issue   `json:"issues"`
}

func (c IssueCache) IsStale(ttl time.Duration) bool {
	return time.Since(c.UpdatedAt) > ttl
}

type RepoCache struct {
	UpdatedAt time.Time `json:"updated_at"`
	Repos     []RepoRef `json:"repos"`
}

func (c RepoCache) IsStale(ttl time.Duration) bool {
	return time.Since(c.UpdatedAt) > ttl
}

func ReadIssueCache(path string) (IssueCache, error) {
	b, err := store.ReadFile(path)
	if err != nil || len(b) == 0 {
		return IssueCache{}, err
	}
	var c IssueCache
	if err := json.Unmarshal(b, &c); err != nil {
		return IssueCache{}, err
	}
	return c, nil
}

func WriteIssueCache(path string, c IssueCache) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

func ReadRepoCache(path string) (RepoCache, error) {
	b, err := store.ReadFile(path)
	if err != nil || len(b) == 0 {
		return RepoCache{}, err
	}
	var c RepoCache
	if err := json.Unmarshal(b, &c); err != nil {
		return RepoCache{}, err
	}
	return c, nil
}

func WriteRepoCache(path string, c RepoCache) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}
