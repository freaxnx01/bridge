// cmd/bridge/clearlock.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// lockClearer is what clearing a lock needs from a forge client. GithubClient
// has both methods; ForgejoClient has no RemoveLabel, so it is refused.
type lockClearer interface {
	RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
	CommentIssue(ctx context.Context, owner, repo string, number int, body string) (forge.Comment, error)
}

// clearEnrichmentLock removes the enrichment-ongoing label from one issue with
// the repo owner's own client, then leaves an audit comment. The comment is
// best effort: the label is the state that matters.
func clearEnrichmentLock(ctx context.Context, resolve clientResolver, repos func() ([]core.Repo, error),
	now func() time.Time, req api.LaunchRequest) error {
	owner, name, _ := strings.Cut(req.Repo, "/")
	all, err := repos()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	i := slices.IndexFunc(all, func(r core.Repo) bool { return r.Owner == owner && r.Name == name })
	if i < 0 {
		return fmt.Errorf("%w: %s", api.ErrUnknownRepo, req.Repo)
	}
	client, err := repoClient(resolve, all[i])
	if errors.Is(err, errNoForgeClient) {
		return fmt.Errorf("%w: %w", api.ErrNoToken, err)
	}
	clearer, ok := client.(lockClearer)
	if !ok {
		return fmt.Errorf("%w: %s", api.ErrUnsupportedForge, all[i].Forge)
	}
	if err := clearer.RemoveLabel(ctx, owner, name, req.Number, cockpit.LabelEnrichmentOngoing); err != nil {
		return fmt.Errorf("remove %s from %s#%d: %w", cockpit.LabelEnrichmentOngoing, req.Repo, req.Number, err)
	}
	body := "🔓 Enrichment lock cleared from the cockpit at " + now().UTC().Format(time.RFC3339)
	if _, err := clearer.CommentIssue(ctx, owner, name, req.Number, body); err != nil {
		slog.Warn("audit comment after clearing lock failed", "repo", req.Repo, "number", req.Number, "error", err)
	}
	return nil
}
