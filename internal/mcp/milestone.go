package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/audit"
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

// clientAs returns the configured client for forgeName/owner asserted to the
// capability T. unsupported completes "does not support ..." when the client
// lacks it.
func clientAs[T any](d Deps, forgeName, owner, unsupported string) (T, error) {
	var zero T
	client := d.ClientFor(forgeName, owner)
	if client == nil {
		return zero, fmt.Errorf("forge %q not configured", forgeName)
	}
	capable, ok := client.(T)
	if !ok {
		return zero, fmt.Errorf("forge %q does not support %s", forgeName, unsupported)
	}
	return capable, nil
}

// lookupMilestone resolves ref against owner/repo's milestones of all states,
// so closed milestones resolve too.
func (d Deps) lookupMilestone(ctx context.Context, forgeName, owner, repo, ref string) (forge.Milestone, error) {
	lister, err := clientAs[milestoneLister](d, forgeName, owner, "milestones")
	if err != nil {
		return forge.Milestone{}, err
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
	lister, err := clientAs[milestoneLister](d, in.Forge, in.Owner, "milestones")
	if err != nil {
		return nil, listMilestonesOutput{}, err
	}
	ms, err := lister.ListMilestones(ctx, in.Owner, in.Repo, state)
	if err != nil {
		return nil, listMilestonesOutput{}, fmt.Errorf("list milestones %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, listMilestonesOutput{Milestones: ms}, nil
}

type setMilestoneInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to assign"`
	Milestone   string `json:"milestone,omitempty" jsonschema:"milestone title or number; exactly one of milestone or clear is required"`
	Clear       bool   `json:"clear,omitempty" jsonschema:"remove the issue's milestone"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without changing anything; set true to apply"`
}

type setMilestoneOutput struct {
	Draft       bool             `json:"draft"`
	Forge       string           `json:"forge"`
	Owner       string           `json:"owner"`
	Repo        string           `json:"repo"`
	IssueNumber int              `json:"issue_number"`
	Milestone   *forge.Milestone `json:"milestone,omitempty"`
	Clear       bool             `json:"clear,omitempty"`
	Issue       *forge.Issue     `json:"issue,omitempty"`
}

// handleSetMilestone assigns, changes or clears an issue's milestone,
// draft-by-default. A draft reads the repo's milestones only to resolve a
// supplied reference; a clear draft makes no network call.
func (d Deps) handleSetMilestone(ctx context.Context, _ *mcp.CallToolRequest, in setMilestoneInput) (*mcp.CallToolResult, setMilestoneOutput, error) {
	if (in.Milestone == "") == !in.Clear {
		return nil, setMilestoneOutput{}, fmt.Errorf("set milestone %s/%s#%d: exactly one of milestone or clear is required", in.Owner, in.Repo, in.IssueNumber)
	}
	out := setMilestoneOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Clear: in.Clear,
	}
	if in.Milestone != "" {
		m, err := d.lookupMilestone(ctx, in.Forge, in.Owner, in.Repo, in.Milestone)
		if err != nil {
			return nil, setMilestoneOutput{}, err
		}
		out.Milestone = &m
	}
	if !in.Confirm {
		return nil, out, nil
	}
	issue, err := d.applyMilestone(ctx, in, out.Milestone)
	if err != nil {
		return nil, setMilestoneOutput{}, err
	}
	out.Draft, out.Issue = false, &issue
	return nil, out, nil
}

// applyMilestone performs the confirmed write and audits its outcome. A nil
// milestone clears.
func (d Deps) applyMilestone(ctx context.Context, in setMilestoneInput, m *forge.Milestone) (forge.Issue, error) {
	setter, err := clientAs[milestoneSetter](d, in.Forge, in.Owner, "milestones")
	if err != nil {
		return forge.Issue{}, err
	}
	number := 0
	if m != nil {
		number = m.Number
	}
	entry := audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "set_milestone", Confirm: true, Outcome: "success"}
	issue, err := setter.SetMilestone(ctx, in.Owner, in.Repo, in.IssueNumber, number)
	if err != nil {
		entry.Outcome = "error"
		d.auditLog(entry)
		return forge.Issue{}, fmt.Errorf("set milestone %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(entry)
	return issue, nil
}
