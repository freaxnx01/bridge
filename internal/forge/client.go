package forge

import (
	"context"
	"errors"
	"time"
)

// ErrRepoExists is returned by CreateRepo when the repo already exists.
var ErrRepoExists = errors.New("repo already exists")

type RepoRef struct {
	Forge         string    `json:"forge"`
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	Description   string    `json:"description,omitempty"`
	Topics        []string  `json:"topics,omitempty"`
	Visibility    string    `json:"visibility,omitempty"`
	HTMLURL       string    `json:"html_url"`
	SSHURL        string    `json:"ssh_url,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type Issue struct {
	Forge   string    `json:"forge"`
	Repo    string    `json:"repo"`
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	URL     string    `json:"url"`
	Labels  []string  `json:"labels,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
}

type Client interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]RepoRef, error)
	ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error)
}
