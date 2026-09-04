package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/launcher"
)

func TestLive_TwoAgents_ReturnsSessionsKeyedBySlotID(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list.json")
	got, err := (&Client{Run: run, Workspace: "w3"}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].SlotID != "bridge" {
		t.Errorf("SlotID = %q, want bridge", got[0].SlotID)
	}
	if got[1].SlotID != "bridge-wt-foo" {
		t.Errorf("SlotID = %q, want bridge-wt-foo", got[1].SlotID)
	}
	if got[0].State != "working" || got[1].State != "blocked" {
		t.Errorf("states = %q/%q, want working/blocked", got[0].State, got[1].State)
	}
	if !got[0].LastActivity.IsZero() {
		t.Error("Herdr reports no timestamps, so LastActivity must stay zero")
	}
}

func TestLive_PaneWithoutAnAgent_IsSkipped(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list_with_bare_pane.json")
	got, err := (&Client{Run: run}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 — an entry with no agent field is not a session", len(got))
	}
	if got[0].SlotID != "bridge" {
		t.Errorf("SlotID = %q", got[0].SlotID)
	}
}

func TestLive_NoAgents_ReturnsEmpty(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list_empty.json")
	got, err := (&Client{Run: run}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestLive_UnmappablePath_IsSkipped(t *testing.T) {
	run := func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"id":"x","result":{"agents":[{"agent":"claude","agent_status":"idle","cwd":"/","pane_id":"w3:p1","tab_id":"w3:t1"}],"type":"agent_list"}}`), nil
	}
	got, err := (&Client{Run: run}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 — a cwd with no slot id is not a session", len(got))
	}
}

func TestAttach_LiveSlot_ReturnsARunPlanThatFocusesTheTab(t *testing.T) {
	body, _ := fixtureRunner(t, "agent_list.json")
	var calls [][]string
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "agent" {
			return body(ctx, args...)
		}
		return []byte(`{"id":"cli:tab:focus","result":{"type":"ok"}}`), nil
	}
	plan, err := (&Client{Run: run, Workspace: "w3"}).Attach("bridge-wt-foo")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if plan.Argv() != nil {
		t.Error("a Herdr attach must be a run plan, never an exec plan — nav must not be replaced")
	}
	fn := plan.Run()
	if fn == nil {
		t.Fatal("Attach returned a plan with no Run func")
	}
	if err := fn(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	last := calls[len(calls)-1]
	if len(last) < 3 || last[0] != "tab" || last[1] != "focus" || last[2] != "w3:t2" {
		t.Errorf("argv = %v, want [tab focus w3:t2]", last)
	}
}

// scriptedRunner replays a response per subcommand and records argv. Keys are
// "<group> <sub>", e.g. "tab create". A key listed in failures fails that many
// times (returning failBody with exit 1) before its normal response, which is
// how the pane-readiness retry is exercised without any real timing.
type scriptedRunner struct {
	mu        sync.Mutex
	responses map[string][]byte
	failures  map[string]int
	failBody  map[string][]byte
	attempts  map[string]int
	calls     [][]string
	// live models the server's own state: agents that `agent start` created.
	// A later `agent list` must see them, or a second launch of the same slot
	// looks unlaunched and the test passes only when goroutines happen to race.
	live    []string // cwd of each started agent
	lastCwd string   // --cwd of the most recent `tab create`
}

// newScriptedRunner builds a runner from testdata files, keyed by subcommand.
func newScriptedRunner(t *testing.T, fixtures map[string]string) *scriptedRunner {
	t.Helper()
	responses := map[string][]byte{}
	for key, file := range fixtures {
		b, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatalf("read fixture %s: %v", file, err)
		}
		responses[key] = b
	}
	return &scriptedRunner{
		responses: responses,
		failures:  map[string]int{},
		failBody:  map[string][]byte{},
		attempts:  map[string]int{},
	}
}

// newLaunchRunner is the common case: a creatable tab and no live agents.
func newLaunchRunner(t *testing.T) *scriptedRunner {
	t.Helper()
	return newScriptedRunner(t, map[string]string{
		"tab create": "tab_create.json",
		"agent list": "agent_list_empty.json",
	})
}

func (s *scriptedRunner) run(_ context.Context, args ...string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, args)
	key := args[0] + " " + args[1]
	s.attempts[key]++
	if n := s.failures[key]; n > 0 {
		s.failures[key] = n - 1
		return s.failBody[key], &ExitError{Code: 1}
	}
	switch key {
	case "tab create":
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--cwd" {
				s.lastCwd = args[i+1]
			}
		}
	case "agent start":
		if s.lastCwd != "" {
			s.live = append(s.live, s.lastCwd)
		}
	case "agent list":
		if len(s.live) > 0 {
			return agentListJSON(s.live), nil
		}
	}
	if body, ok := s.responses[key]; ok {
		return body, nil
	}
	return []byte(`{"id":"x","result":{"type":"ok"}}`), nil
}

// agentListJSON renders an `agent list` envelope for the given agent cwds, in
// the shape captured from the live CLI.
func agentListJSON(cwds []string) []byte {
	var b strings.Builder
	b.WriteString(`{"id":"cli:agent:list","result":{"agents":[`)
	for i, cwd := range cwds {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"agent":"claude","agent_status":"idle","cwd":%q,"pane_id":"w3:p6","tab_id":"w3:t4","workspace_id":"w3"}`, cwd)
	}
	b.WriteString(`],"type":"agent_list"}}`)
	return []byte(b.String())
}

func (s *scriptedRunner) argvFor(group, sub string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.calls {
		if len(a) >= 2 && a[0] == group && a[1] == sub {
			return a
		}
	}
	return nil
}

func TestLaunch_NoLiveSession_CreatesTabStartsAgentThenFocuses(t *testing.T) {
	sr := newLaunchRunner(t)
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if plan.Argv() != nil {
		t.Error("a Herdr launch must be a run plan — nav must not be replaced")
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := sr.argvFor("tab", "create"); got == nil {
		t.Fatal("no `tab create` call")
	}
	start := sr.argvFor("agent", "start")
	if start == nil {
		t.Fatal("no `agent start` call")
	}
	if !containsPair(start, "--pane", "w3:p6") {
		t.Errorf("agent start argv %v must target the created root pane w3:p6", start)
	}
	if !containsPair(start, "--kind", "claude") {
		t.Errorf("agent start argv %v must pass --kind claude", start)
	}
	focus := sr.argvFor("tab", "focus")
	if focus == nil || focus[2] != "w3:t4" {
		t.Errorf("tab focus argv = %v, want [tab focus w3:t4]", focus)
	}
}

func TestLaunch_AgentArgs_ArePassedAfterADoubleDash(t *testing.T) {
	sr := newLaunchRunner(t)
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge",
		agents.AgentSpec{Name: "claude", Bin: "claude", Args: []string{"-n", "bridge [foo]"}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	start := sr.argvFor("agent", "start")
	dash := -1
	for i, a := range start {
		if a == "--" {
			dash = i
			break
		}
	}
	if dash < 0 {
		t.Fatalf("argv %v must separate native agent args with --", start)
	}
	rest := start[dash+1:]
	if len(rest) != 2 || rest[0] != "-n" || rest[1] != "bridge [foo]" {
		t.Errorf("args after -- = %v, want [-n \"bridge [foo]\"]", rest)
	}
}

func TestLaunch_SlotAlreadyLive_FocusesInsteadOfCreatingASecondTab(t *testing.T) {
	sr := newScriptedRunner(t, map[string]string{"agent list": "agent_list.json"})
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge-wt-foo", "/repos/bridge/.worktrees/foo", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if sr.argvFor("tab", "create") != nil {
		t.Error("a live slot must not create a second tab")
	}
	focus := sr.argvFor("tab", "focus")
	if focus == nil || focus[2] != "w3:t2" {
		t.Errorf("tab focus argv = %v, want [tab focus w3:t2]", focus)
	}
}

func TestLaunch_PaneNotReady_RetriesAgentStartThenSucceeds(t *testing.T) {
	sr := newLaunchRunner(t)
	// Fail twice with the real `agent_pane_busy` envelope, then succeed — the
	// shape Herdr returns while a pane is still running shell init. This code
	// was captured from a live server (a tab created --no-focus, occupied with
	// `sleep 120`, then `agent start`); `herdr api schema --json` does not
	// enumerate error codes, so it cannot be looked up.
	sr.failures["agent start"] = 2
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"agent_pane_busy","message":"agent target pane w3:p6 is not an available shell"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := sr.attempts["agent start"]; got != 3 {
		t.Errorf("agent start attempts = %d, want 3 (two failures then success)", got)
	}
	if sr.argvFor("tab", "focus") == nil {
		t.Error("a successful retry must still focus the tab")
	}
}

func TestLaunch_PaneNeverReady_GivesUpAndReportsTheError(t *testing.T) {
	sr := newLaunchRunner(t)
	sr.failures["agent start"] = 99 // never settles
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"agent_pane_busy","message":"agent target pane w3:p6 is not an available shell"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	started := time.Now()
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err == nil {
		t.Fatal("expected an error once the attempts are exhausted")
	}
	if got := sr.attempts["agent start"]; got != startAttempts {
		t.Errorf("agent start attempts = %d, want %d", got, startAttempts)
	}
	// The backoff after the final attempt is skipped, so the whole give-up path
	// must fit inside the sum of the first startAttempts-1 delays.
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("give-up took %v — the sleep after the last attempt was not skipped", elapsed)
	}
	if sr.argvFor("tab", "focus") != nil {
		t.Error("a failed start must not focus a tab with no agent in it")
	}
	if sr.argvFor("tab", "create") == nil {
		t.Error("the created tab is deliberately left in place as a shell in the right directory")
	}
}

func TestLaunch_NonRetryableStartError_FailsImmediatelyWithoutRetrying(t *testing.T) {
	sr := newLaunchRunner(t)
	// A deterministic failure: a bad --kind, a stale pane id, a missing
	// workspace. Retrying cannot help, so the budget must not be spent on it.
	sr.failures["agent start"] = 99
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"agent_kind_unknown","message":"unknown agent kind"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err == nil {
		t.Fatal("expected the error to surface")
	}
	if got := sr.attempts["agent start"]; got != 1 {
		t.Errorf("agent start attempts = %d, want 1 — only a busy pane is retryable", got)
	}
	if sr.argvFor("tab", "focus") != nil {
		t.Error("a failed start must not focus a tab with no agent in it")
	}
}

func TestLaunch_AttachLookupFailsWithRealError_PropagatesWithoutCreatingATab(t *testing.T) {
	// The run-time attach check fails for a reason that is NOT "no session".
	// That must propagate, not fall through into creating a duplicate tab.
	sr := newLaunchRunner(t)
	sr.failures["agent list"] = 99
	sr.failBody["agent list"] = []byte(`{"id":"cli:agent:list","error":{"code":"server_unavailable","message":"herdr server is shutting down"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch must not fail while building the plan: %v", err)
	}
	runErr := plan.Run()(context.Background())
	if runErr == nil {
		t.Fatal("expected the backend error to propagate from the attach lookup")
	}
	if errors.Is(runErr, ErrNoSession) {
		t.Error("a real backend error must not be flattened into ErrNoSession")
	}
	if sr.argvFor("tab", "create") != nil {
		t.Error("a failed lookup must not create a tab — that is how duplicates appear")
	}
}

func TestLaunch_ConcurrentSameSlot_CreatesExactlyOneTab(t *testing.T) {
	// Two Enters on the same row before the first agent registers. Both plans
	// are built while no agent is live, so a build-time precheck would let both
	// through -- the guard has to hold at execution time.
	sr := newLaunchRunner(t)
	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}

	p1, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch 1: %v", err)
	}
	p2, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch 2: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	for _, pl := range []launcher.Plan{p1, p2} {
		go func(pl launcher.Plan) {
			defer wg.Done()
			if err := pl.Run()(context.Background()); err != nil {
				t.Errorf("run: %v", err)
			}
		}(pl)
	}
	wg.Wait()

	creates := 0
	sr.mu.Lock()
	for _, a := range sr.calls {
		if len(a) >= 2 && a[0] == "tab" && a[1] == "create" {
			creates++
		}
	}
	sr.mu.Unlock()
	if creates != 1 {
		t.Errorf("tab create calls = %d, want exactly 1 — a double Enter must not open two tabs", creates)
	}
}

func TestLaunch_SecondLaunchAfterTheFirstCompletes_FocusesInsteadOfCreating(t *testing.T) {
	// The sequential half of the same guarantee. singleflight only collapses
	// genuinely concurrent calls, so once the first launch has finished it is
	// the run-time attach check that must prevent the duplicate.
	sr := newLaunchRunner(t)
	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	spec := agents.AgentSpec{Name: "claude", Bin: "claude"}

	first, err := c.Launch("bridge", "/repos/bridge", spec)
	if err != nil {
		t.Fatalf("Launch 1: %v", err)
	}
	if err := first.Run()(context.Background()); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	second, err := c.Launch("bridge", "/repos/bridge", spec)
	if err != nil {
		t.Fatalf("Launch 2: %v", err)
	}
	if err := second.Run()(context.Background()); err != nil {
		t.Fatalf("run 2: %v", err)
	}

	creates := 0
	sr.mu.Lock()
	for _, a := range sr.calls {
		if len(a) >= 2 && a[0] == "tab" && a[1] == "create" {
			creates++
		}
	}
	sr.mu.Unlock()
	if creates != 1 {
		t.Errorf("tab create calls = %d, want 1 — the second launch must focus the live tab", creates)
	}
}

func TestLaunch_SlotGoesLiveBeforeThePlanRuns_FocusesInsteadOfCreating(t *testing.T) {
	// The plan is built while nothing is live, but by the time it executes an
	// agent exists for the slot. Attach-first must be re-checked at run time.
	sr := newScriptedRunner(t, map[string]string{"tab create": "tab_create.json"})
	empty, err := os.ReadFile(filepath.Join("testdata", "agent_list_empty.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	live, err := os.ReadFile(filepath.Join("testdata", "agent_list.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sr.responses["agent list"] = empty

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge-wt-foo", "/repos/bridge/.worktrees/foo",
		agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// Between building the plan and running it, the session appears.
	sr.responses["agent list"] = live

	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if sr.argvFor("tab", "create") != nil {
		t.Error("the slot went live before the plan ran — it must focus, not create")
	}
	focus := sr.argvFor("tab", "focus")
	if focus == nil || focus[2] != "w3:t2" {
		t.Errorf("tab focus argv = %v, want [tab focus w3:t2]", focus)
	}
}

func TestLaunch_AgentNotReady_FocusesTheTabAndReportsSuccess(t *testing.T) {
	sr := newLaunchRunner(t)
	// Fails once, but with agent_not_ready: the agent DID start and is blocked.
	sr.failures["agent start"] = 1
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"agent_not_ready","message":"agent did not reach a ready state"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("agent_not_ready means the agent exists and needs input — not a failure: %v", err)
	}
	if sr.argvFor("tab", "focus") == nil {
		t.Error("agent_not_ready must still focus the tab so the user can answer the prompt")
	}
	if sr.attempts["agent start"] != 1 {
		t.Errorf("agent start attempts = %d — agent_not_ready must not be retried", sr.attempts["agent start"])
	}
}

func TestStartAgent_GiveUpMessage_NamesTheBusyPane(t *testing.T) {
	sr := newLaunchRunner(t)
	sr.failures["agent start"] = 99
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"agent_pane_busy","message":"agent target pane w3:p6 is not an available shell"}}`)
	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, _ := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	err := plan.Run()(context.Background())
	if err == nil || !errors.Is(err, ErrPaneBusy) {
		t.Fatalf("err = %v, want it to wrap ErrPaneBusy so the cause survives", err)
	}
}

func TestLaunch_CodeAgent_UsesPaneRunAndDoesNotStartAnAgent(t *testing.T) {
	sr := newLaunchRunner(t)
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge", agents.AgentSpec{Name: "code", Bin: "code", Args: []string{"."}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if sr.argvFor("agent", "start") != nil {
		t.Error("code is not a Herdr agent kind — it must not go through agent start")
	}
	pr := sr.argvFor("pane", "run")
	if pr == nil {
		t.Fatal("expected a `pane run` call for the code agent")
	}
	if pr[2] != "w3:p6" {
		t.Errorf("pane run target = %q, want w3:p6", pr[2])
	}
	if pr[3] != "code ." {
		t.Errorf("pane run command = %q, want %q", pr[3], "code .")
	}
	if sr.argvFor("tab", "focus") != nil {
		t.Error("the code GUI takes focus itself — nav must not focus the tab")
	}
}

func TestLaunch_EmptySlotOrDir_IsAnError(t *testing.T) {
	sr := newLaunchRunner(t)
	c := &Client{Run: sr.run, Workspace: "w3"}
	if _, err := c.Launch("", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
		t.Error("expected an error for an empty slot")
	}
	if _, err := c.Launch("bridge", "", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
		t.Error("expected an error for an empty dir")
	}
}

// Compile-time proof that *Client satisfies the seam nav injects.
var _ launcher.Backend = (*Client)(nil)

// countingRunner records every subcommand and can be told to fail the test if
// it is called at all — the way to prove a method performs no I/O.
type countingRunner struct {
	mu    sync.Mutex
	calls [][]string
	body  []byte
}

func (r *countingRunner) run(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	if r.body != nil {
		return r.body, nil
	}
	return []byte(`{"id":"x","result":{"type":"ok"}}`), nil
}

func (r *countingRunner) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestAttach_BuildingThePlan_PerformsNoIO(t *testing.T) {
	// nav calls Attach from inside Update(), the Bubble Tea event loop. Any
	// subprocess call here freezes the UI and hangs nav outright if the herdr
	// server stalls, so the lookup must happen when the plan runs.
	r := &countingRunner{}
	if _, err := (&Client{Run: r.run, Workspace: "w3"}).Attach("bridge"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if n := r.n(); n != 0 {
		t.Errorf("Attach issued %d CLI calls while building the plan; want 0", n)
	}
}

func TestAttach_UnknownSlot_ReportsErrNoSessionWhenThePlanRuns(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list.json")
	plan, err := (&Client{Run: run}).Attach("not-a-live-slot")
	if err != nil {
		t.Fatalf("Attach must not fail while building the plan: %v", err)
	}
	if !errors.Is(plan.Run()(context.Background()), ErrNoSession) {
		t.Error("running the plan for an unknown slot must report ErrNoSession")
	}
}

func TestLaunch_BuildingThePlan_PerformsNoIO(t *testing.T) {
	r := &countingRunner{}
	if _, err := (&Client{Run: r.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if n := r.n(); n != 0 {
		t.Errorf("Launch issued %d CLI calls while building the plan; want 0", n)
	}
}

func TestLaunch_InvalidArguments_StillFailFastWhileBuilding(t *testing.T) {
	// Validation is pure, so it stays synchronous: nav can show it immediately.
	r := &countingRunner{}
	c := &Client{Run: r.run, Workspace: "w3"}
	for _, tt := range []struct{ name, slot, dir string }{
		{"empty slot", "", "/repos/bridge"},
		{"empty dir", "bridge", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Launch(tt.slot, tt.dir, agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
				t.Error("expected a synchronous validation error")
			}
		})
	}
	if n := r.n(); n != 0 {
		t.Errorf("validation issued %d CLI calls; want 0", n)
	}
}
