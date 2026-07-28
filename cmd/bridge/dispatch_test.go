package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

func TestRenderDecisions(t *testing.T) {
	ds := []dispatch.Decision{
		{Candidate: dispatch.Candidate{Repo: "quotes",
			Issue: forge.Issue{Number: 41, Title: "feat: authors filter"}}, Dispatch: true},
		{Candidate: dispatch.Candidate{Repo: "bridge",
			Issue: forge.Issue{Number: 35, Title: "refactor: nav split"}},
			Dispatch: false, Reason: "repo at WIP 1/1"},
	}

	var buf bytes.Buffer
	renderDecisions(&buf, ds)
	out := buf.String()

	if !strings.Contains(out, "quotes") || !strings.Contains(out, "#41") {
		t.Errorf("missing dispatched row:\n%s", out)
	}
	if !strings.Contains(out, "SKIP (repo at WIP 1/1)") {
		t.Errorf("skip reason must be shown:\n%s", out)
	}
	if !strings.Contains(out, "1 dispatched, 1 skipped") {
		t.Errorf("missing summary:\n%s", out)
	}
}

func TestRenderDecisionsEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderDecisions(&buf, nil)
	if !strings.Contains(buf.String(), "0 dispatched, 0 skipped") {
		t.Errorf("got %q", buf.String())
	}
}

func TestCollectCandidatesSkipsNonGithubAndIneligible(t *testing.T) {
	repos := []repoInput{
		{Forge: "github", Owner: "o", Name: "quotes",
			Issues: []forge.Issue{
				{Number: 41, Labels: []string{"feat"}},
				{Number: 42, Labels: []string{"needs-enrichment"}},
			}},
		{Forge: "forgejo", Owner: "f", Name: "notes",
			Issues: []forge.Issue{{Number: 1, Labels: []string{"feat"}}}},
	}

	got := collectCandidates(repos)
	if len(got) != 1 {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	if got[0].Issue.Number != 41 || got[0].Repo != "quotes" {
		t.Errorf("got %+v", got[0])
	}
}
