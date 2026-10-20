package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
)

var (
	// ErrMilestoneNotFound is returned when a milestone reference matches nothing.
	ErrMilestoneNotFound = errors.New("milestone not found")
	// ErrMilestoneAmbiguous is returned when a milestone reference matches several.
	ErrMilestoneAmbiguous = errors.New("milestone reference is ambiguous")
)

// resolveMilestone picks the milestone ref names, by title or number. The
// first rule that yields exactly one match wins; a rule yielding several is
// ambiguous: exact title, case-insensitive title, then number.
func resolveMilestone(ms []forge.Milestone, ref string) (forge.Milestone, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return forge.Milestone{}, notFoundError(ms, ref)
	}
	rules := []func(forge.Milestone) bool{
		func(m forge.Milestone) bool { return m.Title == ref },
		func(m forge.Milestone) bool { return strings.EqualFold(m.Title, ref) },
		numberMatcher(ref),
	}
	for _, rule := range rules {
		matches := filterMilestones(ms, rule)
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		default:
			return forge.Milestone{}, ambiguousError(matches, ref)
		}
	}
	return forge.Milestone{}, notFoundError(ms, ref)
}

func numberMatcher(ref string) func(forge.Milestone) bool {
	n, err := strconv.Atoi(ref)
	if err != nil || n <= 0 {
		return func(forge.Milestone) bool { return false }
	}
	return func(m forge.Milestone) bool { return m.Number == n }
}

func filterMilestones(ms []forge.Milestone, keep func(forge.Milestone) bool) []forge.Milestone {
	var out []forge.Milestone
	for _, m := range ms {
		if keep(m) {
			out = append(out, m)
		}
	}
	return out
}

func notFoundError(ms []forge.Milestone, ref string) error {
	titles := make([]string, 0, len(ms))
	for _, m := range ms {
		titles = append(titles, m.Title)
	}
	available := "none"
	if len(titles) > 0 {
		available = strings.Join(titles, ", ")
	}
	return fmt.Errorf("%w: %q (available: %s)", ErrMilestoneNotFound, ref, available)
}

func ambiguousError(matches []forge.Milestone, ref string) error {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, fmt.Sprintf("%s (#%d, %s)", m.Title, m.Number, m.State))
	}
	return fmt.Errorf("%w: %q matches %s", ErrMilestoneAmbiguous, ref, strings.Join(parts, ", "))
}

// lookupMilestone resolves ref against owner/repo's milestones of all states,
// so closed milestones resolve too.
func (d Deps) lookupMilestone(ctx context.Context, forgeName, owner, repo, ref string) (forge.Milestone, error) {
	client := d.ClientFor(forgeName, owner)
	if client == nil {
		return forge.Milestone{}, fmt.Errorf("forge %q not configured", forgeName)
	}
	lister, ok := client.(milestoneLister)
	if !ok {
		return forge.Milestone{}, fmt.Errorf("forge %q does not support milestones", forgeName)
	}
	ms, err := lister.ListMilestones(ctx, owner, repo, "all")
	if err != nil {
		return forge.Milestone{}, fmt.Errorf("list milestones %s/%s: %w", owner, repo, err)
	}
	m, err := resolveMilestone(ms, ref)
	if err != nil {
		return forge.Milestone{}, fmt.Errorf("resolve milestone in %s/%s: %w", owner, repo, err)
	}
	return m, nil
}

type listMilestonesInput struct {
	Forge string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner string `json:"owner" jsonschema:"repository owner"`
	Repo  string `json:"repo" jsonschema:"repository name"`
	State string `json:"state,omitempty" jsonschema:"open (default), closed or all"`
}

type listMilestonesOutput struct {
	Milestones []forge.Milestone `json:"milestones"`
}

func (d Deps) handleListMilestones(ctx context.Context, _ *mcp.CallToolRequest, in listMilestonesInput) (*mcp.CallToolResult, listMilestonesOutput, error) {
	state := in.State
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		return nil, listMilestonesOutput{}, fmt.Errorf("invalid state %q: must be open, closed or all", in.State)
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, listMilestonesOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	lister, ok := client.(milestoneLister)
	if !ok {
		return nil, listMilestonesOutput{}, fmt.Errorf("forge %q does not support milestones", in.Forge)
	}
	ms, err := lister.ListMilestones(ctx, in.Owner, in.Repo, state)
	if err != nil {
		return nil, listMilestonesOutput{}, fmt.Errorf("list milestones %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, listMilestonesOutput{Milestones: ms}, nil
}
