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

// issueCreator is asserted by create_issue.
type issueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// repoCreator is asserted by create_repo.
type repoCreator interface {
	CreateRepo(ctx context.Context, name string, private bool) (forge.RepoRef, error)
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
	if _, ok := r.(issueCreator); ok {
		capabilities = append(capabilities, "create_issue")
	}
	if _, ok := r.(repoCreator); ok {
		capabilities = append(capabilities, "create_repo")
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
	DefaultOwners    []Target
	ClientFor        func(forgeName, owner string) ForgeReader
	BuildOverview    func(ctx context.Context) (overview.Snapshot, error)
	Audit            *audit.Logger
}

// auditLog appends e to Deps.Audit. A no-op when Audit is nil (tests, or a
// caller that hasn't wired one up) so handlers never need a nil check.
func (d Deps) auditLog(e audit.Entry) {
	if d.Audit == nil {
		return
	}
	d.Audit.Log(e)
}

// targets returns the (forge, owner) pairs list_repos should query for the
// given input: an explicit owner requires an explicit forge (an owner given
// without a forge is ambiguous across github/forgejo and is rejected rather
// than silently guessed); otherwise the configured defaults are used,
// narrowed by an optional forge.
func (d Deps) targets(in listReposInput) ([]Target, error) {
	if in.Owner != "" {
		if in.Forge == "" {
			return nil, fmt.Errorf("owner %q given without forge: specify forge (github or forgejo)", in.Owner)
		}
		return []Target{{Forge: in.Forge, Owner: in.Owner}}, nil
	}
	var out []Target
	for _, t := range d.DefaultOwners {
		if in.Forge != "" && t.Forge != in.Forge {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}
