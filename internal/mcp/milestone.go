package mcp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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
