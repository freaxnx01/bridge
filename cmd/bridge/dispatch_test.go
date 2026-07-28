package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestDispatchSubcommands_InheritPersistentFlags is a regression guard for the
// unreachable-flag bug: --dry-run/--json/--auto were registered as LOCAL flags
// on dispatchCmd, which cobra does not propagate to subcommands, so `dispatch
// status --json` failed with "unknown flag". Driving the real command tree
// through rootCmd.Execute() (not calling runDispatchStatus directly) is what
// would have caught it — cobra's ExecuteC always runs on the root command
// regardless of which node Execute is called on, so args/output must be set
// on rootCmd itself for this to exercise the real parse path.
func TestDispatchSubcommands_InheritPersistentFlags(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { dispatchJSON, dispatchDryRun, dispatchAuto = false, false, false })

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"dispatch", "status", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("bridge dispatch status --json: %v", err)
	}

	var got struct {
		Paused bool `json:"paused"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
}

// writeFakeGithubRepo creates a single bare `github/<owner>/public/<name>` git
// checkout under a fresh temp root, matching the layout discoverAllRoots
// expects. Kept minimal (one repo only) so the fake GitHub server in
// TestRunDispatch_FullPipeline_AppliesOnlyEligibleLabel only ever sees
// requests for that one repo.
func writeFakeGithubRepo(t *testing.T, owner, name string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "github", owner, "public", name, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRunDispatch_FullPipeline_AppliesOnlyEligibleLabel is an integration test
// over the real pipeline: fetchRepoInputs -> collectCandidates -> Order ->
// ApplyCaps -> applyDecisions, against a fake GitHub API. It covers two gaps
// the existing tests left: applyDecisions' actual write path (previously
// untested — only the zero-candidate path was exercised), and the Critical-1
// "already dispatched" guard (an issue already carrying ai-implement, with no
// open PR, must not be re-labeled/re-commented on a later tick).
func TestRunDispatch_FullPipeline_AppliesOnlyEligibleLabel(t *testing.T) {
	var gotLabelBody map[string]any
	var labelCalls, commentCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/issues":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"number":41,"title":"eligible issue","html_url":"u41","labels":[{"name":"feat"}],"updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z"},
				{"number":42,"title":"already dispatched","html_url":"u42","labels":[{"name":"ai-implement"}],"updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z"}
			]`))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/milestones":
			w.Write([]byte(`[]`))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/pulls":
			w.Write([]byte(`[]`))
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/issues/41/labels":
			labelCalls++
			_ = json.NewDecoder(r.Body).Decode(&gotLabelBody)
			w.Write([]byte(`[{"name":"ai-implement"}]`))
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/issues/41/comments":
			commentCalls++
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":1,"body":"x","created_at":"2026-07-01T00:00:00Z"}`))
		case r.URL.Path == "/repos/freaxnx01/bridge/issues/42/labels" || r.URL.Path == "/repos/freaxnx01/bridge/issues/42/comments":
			t.Errorf("issue #42 already carries ai-implement — must not be re-dispatched, got %s %s", r.Method, r.URL.Path)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	root := writeFakeGithubRepo(t, "freaxnx01", "bridge")
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "tok")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	setDispatchFlags(t, false, false)

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runDispatch(cmd, nil); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	if labelCalls != 1 {
		t.Errorf("want exactly 1 label call for #41, got %d", labelCalls)
	}
	if commentCalls != 1 {
		t.Errorf("want exactly 1 comment call for #41, got %d", commentCalls)
	}
	if gotLabelBody == nil {
		t.Fatal("no label body captured")
	}
	labels, _ := gotLabelBody["labels"].([]any)
	if len(labels) != 1 || labels[0] != "ai-implement" {
		t.Errorf("want labels == [\"ai-implement\"], got %v", gotLabelBody["labels"])
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
