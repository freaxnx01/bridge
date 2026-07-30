package forge

import (
	"context"
	"encoding/json"
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

// isEmptyRepoMessage reports whether a 404/409 error body from a contents or
// git-data endpoint describes a repo with no commits yet, as opposed to a
// missing path. Both GitHub and Forgejo phrase this as an "message" field
// containing "empty" ("This repository is empty." / "repository is empty"),
// so a substring check covers both without hardcoding either forge's exact
// wording.
func isEmptyRepoMessage(body []byte) bool {
	var m struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(m.Message), "empty")
}

// treeEntryType normalizes a git tree entry's (type, mode) pair — "blob"/
// "tree"/"commit" per the Git Trees API — into the file/dir/symlink/submodule
// vocabulary the contents API uses natively, so ListTree reports one
// consistent Type regardless of which API shape answered the call.
func treeEntryType(apiType, mode string) string {
	switch apiType {
	case "tree":
		return "dir"
	case "commit":
		return "submodule"
	case "blob":
		if mode == "120000" {
			return "symlink"
		}
		return "file"
	default:
		return apiType
	}
}

type RepoRef struct {
	Forge         string    `json:"forge"`
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	Description   string    `json:"description,omitempty"`
	Topics        []string  `json:"topics,omitempty"`
	Visibility    string    `json:"visibility,omitempty"`
	Archived      bool      `json:"archived,omitempty"`
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

// TreeEntry is one path in a repo tree listing. Type is one of "file", "dir",
// "symlink", or "submodule" — normalized across GitHub's contents/trees API
// shapes and Forgejo's equivalent.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
	SHA  string `json:"sha"`
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
