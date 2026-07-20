package mcp

import (
	"context"
	"fmt"

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
	return capabilities
}

// Deps are the injected dependencies of the MCP server. ClientFor returns a
// ready per-(forge, owner) reader (token baked in) or nil when that forge is
// unconfigured. BuildOverview produces the cross-forge status snapshot.
type Deps struct {
	ReadOnly      bool
	DefaultOwners []Target
	ClientFor     func(forgeName, owner string) ForgeReader
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
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
