package navdata

import (
	"context"
	"errors"
	"fmt"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/worktree"
)

// CloneFunc clones a remote ref and returns the local repo. The cmd layer
// supplies it (direnv-loaded credentials live there, not here).
type CloneFunc func(ctx context.Context, ref forge.RepoRef) (core.Repo, error)

// CreateRepoFunc creates a repo on forgeName (forgejo|github) at the given
// visibility, clones it, and returns the local repo. Supplied by the cmd layer.
type CreateRepoFunc func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error)

// RepoChoice is one forge × visibility option of the new-repo flow.
type RepoChoice struct {
	Label   string
	Forge   string
	Private bool
}

// RepoChoices are the new-repo options in display order.
func RepoChoices() []RepoChoice {
	return []RepoChoice{
		{"Forgejo · Private", "forgejo", true},
		{"Forgejo · Public", "forgejo", false},
		{"GitHub · Private", "github", true},
		{"GitHub · Public", "github", false},
	}
}

// CreateWorktree resolves worktree name of repo, creating
// <repo>/.worktrees/<name> on branch worktree-<name> when it does not exist,
// and returns the row to launch. label overrides the session display name
// ("" keeps "<repo> [<name>]"). worktree.Resolve's error is returned as is: it
// already names the git step, and nav shows its text verbatim.
func CreateWorktree(ctx context.Context, repo core.Repo, name, label string) (DashRow, error) {
	if err := ctx.Err(); err != nil {
		return DashRow{}, err
	}
	dir, _, err := worktree.Resolve(worktree.ExecRunner{}, repo.Path, name)
	if err != nil {
		return DashRow{}, err
	}
	return DashRow{Worktree: name, Branch: "worktree-" + name, Path: dir, DisplayLabel: label}, nil
}

// WorktreeErrMessage maps a CreateWorktree error to a user-facing line: a
// friendly hint for "target dir already exists", the raw text otherwise.
func WorktreeErrMessage(err error) string {
	var wex *worktree.WorktreeExistsError
	if errors.As(err, &wex) {
		return fmt.Sprintf("worktree %q already exists — pick a different name, or remove .worktrees/%s", wex.Name, wex.Name)
	}
	return err.Error()
}
