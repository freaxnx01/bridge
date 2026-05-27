package main

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestFindReposByKeywordNameTakesPrecedence(t *testing.T) {
	// A name hit on "nextgen" must short-circuit before meta hits — a typo
	// like "nextgne" must NOT silently land on a meta-tagged repo just
	// because the basename match is empty when both can be present.
	repos := []core.Repo{
		{Name: "ArchiveRestApiNextGen", Topics: []string{"rest", "api"}},
		{Name: "mgrabber-nextgen", Topics: []string{"crawler"}},
		{Name: "unrelated", Desc: "talks about nextgen tooling"},
	}
	got := findReposByKeyword(repos, "nextgen")
	if len(got) != 2 {
		t.Fatalf("expected 2 name hits, got %d (%v)", len(got), got)
	}
	for _, r := range got {
		if r.Name == "unrelated" {
			t.Errorf("meta-only match leaked into a name-hit result set: %v", got)
		}
	}
}

func TestFindReposByKeywordMetaFallback(t *testing.T) {
	// Empty name match → fall back to Desc and Topics.
	repos := []core.Repo{
		{Name: "ArchiveRestApi", Topics: []string{"rest", "nextgen"}},
		{Name: "Other", Desc: "next-gen pipeline"},
		{Name: "noise", Desc: "unrelated"},
	}
	got := findReposByKeyword(repos, "nextgen")
	if len(got) != 1 || got[0].Name != "ArchiveRestApi" {
		t.Errorf("expected single match on ArchiveRestApi via topic, got %v", got)
	}

	got = findReposByKeyword(repos, "next-gen")
	if len(got) != 1 || got[0].Name != "Other" {
		t.Errorf("expected single match on Other via desc, got %v", got)
	}
}

func TestFindReposByKeywordEmptyOnNoMatch(t *testing.T) {
	repos := []core.Repo{{Name: "a"}, {Name: "b", Topics: []string{"x"}}}
	if got := findReposByKeyword(repos, "zzz"); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestFindReposByKeywordCaseInsensitive(t *testing.T) {
	repos := []core.Repo{
		{Name: "MyRepo", Topics: []string{"NextGen"}},
	}
	if got := findReposByKeyword(repos, "NEXTGEN"); len(got) != 1 {
		t.Errorf("case-insensitive meta lookup failed: %v", got)
	}
}
