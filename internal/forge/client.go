package forge

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// ErrRepoExists is returned by CreateRepo when the repo already exists.
var ErrRepoExists = errors.New("repo already exists")

// escapePathSegments percent-escapes each "/"-delimited segment of p so a
// segment cannot reinterpret the request URL (inject a query string,
// fragment, or extra path segment) while preserving p's directory structure.
func escapePathSegments(p string) string {
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

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
	Forge     string    `json:"forge"`
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	Milestone string    `json:"milestone,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
	Created   time.Time `json:"created,omitempty"`
}

// Comment is a single issue comment.
type Comment struct {
	ID      int       `json:"id"`
	Body    string    `json:"body"`
	Created time.Time `json:"created,omitempty"`
}

// Milestone is an open milestone. DueOn is the zero time when unset.
type Milestone struct {
	Number int       `json:"number"`
	Title  string    `json:"title"`
	DueOn  time.Time `json:"due_on,omitempty"`
}

// PullRequest is an open pull request. Body is needed to resolve "Closes #N".
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
}

type Client interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]RepoRef, error)
	ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error)
}
