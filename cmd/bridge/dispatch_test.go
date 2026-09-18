package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/usage"
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
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))

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
	t.Setenv("BRIDGE_BASE", t.TempDir())                                     // exists but has no repos: fetchRepoInputs finds nothing
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent")) // never touch the real ~/.claude in a test
	setDispatchFlags(t, true, false)                                         // --json, no --dry-run

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
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
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
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
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
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
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

func TestMeasureWindowUSDCountsBothSources(t *testing.T) {
	projects := t.TempDir()
	cache := t.TempDir()
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", projects)
	t.Setenv("XDG_CACHE_HOME", cache)

	now := time.Now().UTC()

	// One interactive turn: 1M output tokens of opus at 75 USD/Mtok.
	line := `{"type":"assistant","timestamp":"` + now.Add(-time.Hour).Format(time.RFC3339) +
		`","message":{"model":"claude-opus-4-7","usage":{"input_tokens":0,"output_tokens":1000000,` +
		`"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
	dir := filepath.Join(projects, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// One dispatched run in the ledger.
	var l usage.Ledger
	l.Append(usage.Run{At: now.Add(-time.Minute), Repo: "bridge", Issue: 254, EstUSD: 2})
	if err := usage.WriteLedger(dispatchLedgerPath(), l); err != nil {
		t.Fatal(err)
	}

	got, ok := measureWindowUSD(context.Background(), dispatch.DefaultConfig(), now.Add(-5*time.Hour), now)
	if !ok {
		t.Fatal("measurement should be known")
	}
	if diff := got - 77.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("want 75 interactive + 2 pipeline = 77, got %v", got)
	}
}

func TestMeasureWindowUSDMissingSourcesAreZeroNotUnknown(t *testing.T) {
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	got, ok := measureWindowUSD(context.Background(), dispatch.DefaultConfig(), time.Now().Add(-5*time.Hour), time.Now())
	if !ok || got != 0 {
		t.Errorf("a fresh install has measured zero usage, not unknown: got=%v ok=%v", got, ok)
	}
}

func TestMeasureWindowUSDUnreadableLedgerIsUnknown(t *testing.T) {
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	// Corrupt ledger: readable file, invalid JSON.
	path := dispatchLedgerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := measureWindowUSD(context.Background(), dispatch.DefaultConfig(), time.Now().Add(-5*time.Hour), time.Now()); ok {
		t.Error("a corrupt ledger must report unknown so the rung fails closed")
	}
}

func TestTranscriptRootHonoursTheEnvOverride(t *testing.T) {
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", "/tmp/somewhere")
	if got := transcriptRoot(); got != "/tmp/somewhere" {
		t.Errorf("got %q", got)
	}
}

func TestRunDispatchStatusReportsBudget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := runDispatchStatus(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "budget window:") {
		t.Errorf("status must report the budget window:\n%s", out)
	}
	if !strings.Contains(out, "9.60") {
		t.Errorf("status must show the limit (12.00 * 0.80):\n%s", out)
	}
}

func TestRunDispatchStatusJSONCarriesBudgetFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))

	dispatchJSON = true
	t.Cleanup(func() { dispatchJSON = false })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := runDispatchStatus(cmd, nil); err != nil {
		t.Fatal(err)
	}

	var got struct {
		BudgetUsedUSD  float64 `json:"budget_used_usd"`
		BudgetLimitUSD float64 `json:"budget_limit_usd"`
		BudgetKnown    bool    `json:"budget_known"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if diff := got.BudgetLimitUSD - 9.6; diff > 0.0001 || diff < -0.0001 || !got.BudgetKnown {
		t.Errorf("%+v", got)
	}
}

func TestRunDispatchAutoOutsideWindowSkipsBeforeFetching(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// A window that cannot contain "now": one minute wide, an hour ago.
	past := time.Now().Add(-time.Hour)
	body := `{"schedule":{"windows":[{"from":"` + past.Format("15:04") + `","to":"` +
		past.Add(time.Minute).Format("15:04") + `","budget_rung":false}]}}`
	if err := os.MkdirAll(filepath.Join(cfgDir, "bridge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "bridge", "dispatch.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	dispatchAuto = true
	t.Cleanup(func() { dispatchAuto = false })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	// No forge client is configured here: reaching the fetch would fail, so a
	// clean return is itself the assertion that the gate ran first.
	if err := runDispatch(cmd, nil); err != nil {
		t.Fatalf("out-of-window tick must return cleanly: %v", err)
	}
	if !strings.Contains(buf.String(), "outside dispatch window") {
		t.Errorf("got %q", buf.String())
	}
}

// recordRuns is what the budget rung trusts to know what the pipeline spent,
// so it needs a direct assertion rather than incidental execution.
func TestRecordRunsBooksEveryRunAndPrunesOldHistory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	now := time.Now().UTC()
	path := dispatchLedgerPath()

	// Pre-existing history: one entry inside the retention window, one outside.
	seed := usage.Ledger{Runs: []usage.Run{
		{At: now.Add(-2 * time.Hour), Repo: "old-but-kept", Issue: 1, EstUSD: 9},
		{At: now.AddDate(0, 0, -30), Repo: "expired", Issue: 2, EstUSD: 9},
	}}
	if err := usage.WriteLedger(path, seed); err != nil {
		t.Fatal(err)
	}

	runs := []usage.Run{
		{At: now, Repo: "bridge", Issue: 254, EstUSD: 2},
		{At: now, Repo: "quotes", Issue: 41, EstUSD: 2},
	}
	if err := recordRuns(path, runs, now); err != nil {
		t.Fatal(err)
	}

	got, err := usage.LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 3 {
		t.Fatalf("want 2 new + 1 retained = 3 runs, got %d: %+v", len(got.Runs), got.Runs)
	}
	byIssue := map[int]usage.Run{}
	for _, r := range got.Runs {
		byIssue[r.Issue] = r
	}
	for _, n := range []int{254, 41} {
		if r, ok := byIssue[n]; !ok || r.EstUSD != 2 {
			t.Errorf("issue %d not booked at the mean: %+v (ok=%v)", n, r, ok)
		}
	}
	if byIssue[254].Repo != "bridge" || byIssue[41].Repo != "quotes" {
		t.Errorf("repos mismatched: %+v", got.Runs)
	}
	if _, expired := byIssue[2]; expired {
		t.Error("a 30-day-old run must be pruned")
	}
	if _, kept := byIssue[1]; !kept {
		t.Error("a 2-hour-old run must be retained")
	}
	if sum := got.SumSince(now.Add(-5 * time.Hour)); sum != 13 {
		t.Errorf("trailing-window sum = %v, want 9 + 2 + 2 = 13", sum)
	}
}

func TestRecordRunsNoRunsLeavesTheLedgerAlone(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := recordRuns(dispatchLedgerPath(), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dispatchLedgerPath()); !os.IsNotExist(err) {
		t.Errorf("an empty tick must not create the ledger file: %v", err)
	}
}

// The quota window is rolling, so spend late in the night is still inside it
// when the operator starts work. resolveTiming must therefore arm the rung in
// the shoulder before a rung window and measure only the span that survives to
// the handover.
func TestResolveTimingGuardsTheMorningHandover(t *testing.T) {
	cfg := dispatch.DefaultConfig() // 18:00-07:00 no rung, 07:00-18:00 rung; 5h window
	day := func(h, m int) time.Time { return time.Date(2026, 9, 18, h, m, 0, 0, time.Local) }

	tests := []struct {
		name            string
		now             time.Time
		wantRung        bool
		wantMeasureFrom time.Time
		wantNightCap    bool
	}{
		{
			name: "deep night burns freely", now: day(23, 0),
			wantRung: false, wantNightCap: true,
		},
		{
			// 01:00 + 5h = 06:00, aged out before the handover.
			name: "01:00 is still outside the shoulder", now: day(1, 0),
			wantRung: false, wantNightCap: true,
		},
		{
			// 05:00 + 5h = 10:00: this spend is in the operator's window at
			// 07:00, so it is measured against the 02:00-07:00 span.
			name: "05:00 is guarded for the 07:00 handover", now: day(5, 0),
			wantRung: true, wantMeasureFrom: day(2, 0), wantNightCap: true,
		},
		{
			name: "midday measures the plain trailing window", now: day(12, 0),
			wantRung: true, wantMeasureFrom: day(7, 0), wantNightCap: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTiming(cfg, tc.now)
			if got.RungOn != tc.wantRung {
				t.Fatalf("RungOn=%v want %v", got.RungOn, tc.wantRung)
			}
			if tc.wantRung && !got.MeasureFrom.Equal(tc.wantMeasureFrom) {
				t.Errorf("MeasureFrom=%v want %v", got.MeasureFrom, tc.wantMeasureFrom)
			}
			if got.NightCapApplies != tc.wantNightCap {
				t.Errorf("NightCapApplies=%v want %v", got.NightCapApplies, tc.wantNightCap)
			}
		})
	}
}

// The concrete failure the shoulder exists to stop: the night has already
// spent the operator's window, and a 05:00 tick must refuse rather than hand
// over a window with no headroom left.
func TestShoulderRefusesWhenTheHandoverWindowIsAlreadySpent(t *testing.T) {
	cfg := dispatch.DefaultConfig()
	timing := resolveTiming(cfg, time.Date(2026, 9, 18, 5, 0, 0, 0, time.Local))
	if !timing.RungOn {
		t.Fatal("05:00 must be guarded")
	}

	// $9.00 already spent inside the 02:00-07:00 span; the limit is 12.00*0.80.
	budget := dispatch.NewBudgetState(cfg.Budget, timing.RungOn, 9.0, true)
	ds := dispatch.ApplyCaps(
		[]dispatch.Candidate{{Repo: "bridge", Issue: forge.Issue{Number: 254}}},
		cfg,
		dispatch.Counts{NightCapApplies: timing.NightCapApplies},
		budget,
	)
	if ds[0].Dispatch {
		t.Errorf("a run projected at $2 on top of $9 would cross $9.60: %+v", ds[0])
	}
	if !strings.HasPrefix(ds[0].Reason, "budget-exhausted") {
		t.Errorf("reason = %q", ds[0].Reason)
	}
}

// And the converse: an unspent handover window still lets the night work.
func TestShoulderAllowsWhenTheHandoverWindowHasHeadroom(t *testing.T) {
	cfg := dispatch.DefaultConfig()
	timing := resolveTiming(cfg, time.Date(2026, 9, 18, 5, 0, 0, 0, time.Local))
	budget := dispatch.NewBudgetState(cfg.Budget, timing.RungOn, 1.0, true)
	ds := dispatch.ApplyCaps(
		[]dispatch.Candidate{{Repo: "bridge", Issue: forge.Issue{Number: 254}}},
		cfg,
		dispatch.Counts{NightCapApplies: timing.NightCapApplies},
		budget,
	)
	if !ds[0].Dispatch {
		t.Errorf("$1 spent leaves room for a $2 run under $9.60: %+v", ds[0])
	}
}
