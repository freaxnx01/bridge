package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/spf13/cobra"
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

func TestSetPausedTogglesState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := setPaused(cmd, true); err != nil {
		t.Fatalf("setPaused(true): %v", err)
	}
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !state.Paused {
		t.Fatalf("want paused=true, got %+v", state)
	}
	if !strings.Contains(out.String(), "paused") {
		t.Errorf("expected confirmation message, got %q", out.String())
	}

	if err := setPaused(cmd, false); err != nil {
		t.Fatalf("setPaused(false): %v", err)
	}
	state, err = dispatch.ReadState(dispatchStatePath())
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Paused {
		t.Fatalf("want paused=false, got %+v", state)
	}
}

func TestRunDispatchStatusJSON(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := dispatch.WriteState(dispatchStatePath(), dispatch.State{Paused: true, DispatchedTonight: 2}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	dispatchJSON = true
	t.Cleanup(func() { dispatchJSON = false })

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runDispatchStatus(cmd, nil); err != nil {
		t.Fatalf("runDispatchStatus: %v", err)
	}

	var got struct {
		Paused bool `json:"paused"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
	if !got.Paused {
		t.Errorf("expected paused=true in JSON output, got %q", out.String())
	}
}

// setDispatchFlags sets the package-level dispatch flags for the duration of
// a test and restores their zero values afterward, since runDispatch reads
// them as globals rather than taking parameters.
func setDispatchFlags(t *testing.T, jsonOut, dryRun bool) {
	t.Helper()
	dispatchJSON, dispatchDryRun = jsonOut, dryRun
	t.Cleanup(func() { dispatchJSON, dispatchDryRun = false, false })
}

func TestRunDispatch_JSONLiveTick_CallsApplyDecisions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BRIDGE_BASE", t.TempDir()) // exists but has no repos: fetchRepoInputs finds nothing
	setDispatchFlags(t, true, false)     // --json, no --dry-run

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runDispatch(cmd, nil); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	var decisions []dispatch.Decision
	if err := json.Unmarshal(out.Bytes(), &decisions); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}

	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.LastTick.IsZero() {
		t.Errorf("want LastTick set (applyDecisions ran on a live --json tick), got zero value")
	}
}

func TestRunDispatch_DryRunJSON_SkipsApplyDecisions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BRIDGE_BASE", t.TempDir())
	setDispatchFlags(t, true, true) // --json --dry-run: dry-run always wins

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runDispatch(cmd, nil); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !state.LastTick.IsZero() {
		t.Errorf("want LastTick unset (--dry-run must skip applyDecisions), got %v", state.LastTick)
	}
}
