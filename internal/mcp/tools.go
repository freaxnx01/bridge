package mcp

import (
	"context"
	"fmt"

	"github.com/freaxnx01/bridge/internal/audit"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

// Target is a (forge, owner) pair queried by list_repos when no owner is given
// in the tool input.
type Target struct {
	Forge string
	Owner string
}

// ForgeReader is the tier-1 surface every forge client satisfies. Deps.ClientFor
// returns this, and handlers needing more assert for one of the capability
// interfaces below.
type ForgeReader interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
	ListOpenIssues(ctx context.Context, owner, repo string) ([]forge.Issue, error)
}

// fileReader is asserted by read_file.
type fileReader interface {
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
}

// fileWriter is asserted by put_file. GetFile is used first to detect an
// existing file at the target path — an update without a matching sha is
// rejected before PutFile is ever called, rather than surfacing a raw
// forge-API error.
type fileWriter interface {
	fileReader
	PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (htmlURL string, err error)
}

// treeLister is asserted by list_tree.
type treeLister interface {
	ListTree(ctx context.Context, owner, repo, path string, recursive bool) (entries []forge.TreeEntry, truncated bool, err error)
}

// searchCoder is asserted by search_code. No forge client is required to
// implement it — Forgejo has no code-search REST API (only an HTML search
// page), so ForgejoClient deliberately does not satisfy this interface, and
// Capabilities()/list_git_forges report that gap honestly per forge rather
// than silently returning zero results.
type searchCoder interface {
	SearchCode(ctx context.Context, owner, repo, query string) (matches []forge.CodeMatch, incomplete bool, err error)
}

// issueCreator is asserted by create_issue.
type issueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// repoCreator is asserted by create_repo.
type repoCreator interface {
	CreateRepo(ctx context.Context, name string, private bool) (forge.RepoRef, error)
}

// repoUpdater is asserted by update_repo for its description/private/archived
// fields. A nil pointer leaves that field unchanged.
type repoUpdater interface {
	UpdateRepo(ctx context.Context, owner, repo string, description *string, private, archived *bool) (forge.RepoRef, error)
}

// topicsSetter is asserted by update_repo for its topics field — both forges
// expose topics via a separate endpoint from the main repo PATCH.
type topicsSetter interface {
	SetTopics(ctx context.Context, owner, repo string, topics []string) ([]string, error)
}

// issueCloser is asserted by close_issue.
type issueCloser interface {
	CloseIssue(ctx context.Context, owner, repo string, number int, stateReason string) (forge.Issue, error)
}

// issueUpdater is asserted by update_issue.
type issueUpdater interface {
	UpdateIssue(ctx context.Context, owner, repo string, number int, title, body *string) (forge.Issue, error)
}

// labelAdder is asserted by add_labels.
type labelAdder interface {
	AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error)
}

// issueCommenter is asserted by comment_issue.
type issueCommenter interface {
	CommentIssue(ctx context.Context, owner, repo string, number int, body string) (forge.Comment, error)
}

// repoArchiver and repoDeleter are tier-3/4 capability stubs: declared so
// Capabilities' switch is complete before those tiers are implemented, but no
// concrete client satisfies them yet.
type repoArchiver interface {
	ArchiveRepo(ctx context.Context, owner, repo string) (forge.RepoRef, error)
}

type repoDeleter interface {
	DeleteRepo(ctx context.Context, owner, repo string) error
}

// Capabilities returns the names of the MCP tools a resolved client supports.
// It reports tool names rather than method names so a caller can map the result
// directly onto what it may invoke. Returns nil for a nil reader.
//
// Write capabilities are reported regardless of Deps.ReadOnly; filtering them
// to what is actually registered is the caller's job.
func Capabilities(r ForgeReader) []string {
	if r == nil {
		return nil
	}
	capabilities := []string{"list_repos", "list_issues"}
	if _, ok := r.(fileReader); ok {
		capabilities = append(capabilities, "read_file")
	}
	if _, ok := r.(fileWriter); ok {
		capabilities = append(capabilities, "put_file")
	}
	if _, ok := r.(treeLister); ok {
		capabilities = append(capabilities, "list_tree")
	}
	if _, ok := r.(searchCoder); ok {
		capabilities = append(capabilities, "search_code")
	}
	if _, ok := r.(issueCreator); ok {
		capabilities = append(capabilities, "create_issue")
	}
	if _, ok := r.(repoCreator); ok {
		capabilities = append(capabilities, "create_repo")
	}
	if _, ok := r.(repoUpdater); ok {
		capabilities = append(capabilities, "update_repo")
	}
	if _, ok := r.(issueCloser); ok {
		capabilities = append(capabilities, "close_issue")
	}
	if _, ok := r.(issueUpdater); ok {
		capabilities = append(capabilities, "update_issue")
	}
	if _, ok := r.(labelAdder); ok {
		capabilities = append(capabilities, "add_labels")
	}
	if _, ok := r.(issueCommenter); ok {
		capabilities = append(capabilities, "comment_issue")
	}
	if _, ok := r.(repoArchiver); ok {
		capabilities = append(capabilities, "archive_repo")
	}
	if _, ok := r.(repoDeleter); ok {
		capabilities = append(capabilities, "delete_repo")
	}
	return capabilities
}

// Deps are the injected dependencies of the MCP server. ClientFor returns a
// ready per-(forge, owner) reader (token baked in) or nil when that forge is
// unconfigured. BuildOverview produces the cross-forge status snapshot.
type Deps struct {
	ReadOnly         bool
	AllowDestructive bool
	// PathAllowlist gates which paths put_file may write to. A nil/empty
	// value falls back to DefaultPathAllowlist inside handlePutFile.
	PathAllowlist PathAllowlist
	DefaultOwners []Target
	ClientFor     func(forgeName, owner string) ForgeReader
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
	Audit         *audit.Logger
}

// auditLog appends e to Deps.Audit. A no-op when Audit is nil (tests, or a
// caller that hasn't wired one up) so handlers never need a nil check.
func (d Deps) auditLog(e audit.Entry) {
	if d.Audit == nil {
		return
	}
	d.Audit.Log(e)
}

// targets returns the (forge, owner) pairs a fan-out tool (list_repos,
// search_code) should query for the given forge/owner filter: an explicit
// owner requires an explicit forge (an owner given without a forge is
// ambiguous across github/forgejo and is rejected rather than silently
// guessed); otherwise the configured defaults are used, narrowed by an
// optional forge.
func (d Deps) targets(forge, owner string) ([]Target, error) {
	if owner != "" {
		if forge == "" {
			return nil, fmt.Errorf("owner %q given without forge: specify forge (github or forgejo)", owner)
		}
		return []Target{{Forge: forge, Owner: owner}}, nil
	}
	var out []Target
	for _, t := range d.DefaultOwners {
		if forge != "" && t.Forge != forge {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}
