# Agents view — live Claude sessions (#170) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface every live Claude Code session across all repos — status, kind, repo (from `cwd`), age — as a read-only view on both the nav TUI (a new screen) and the WebUI (a new section), wrapping `claude agents --json`.

**Architecture:** A new `internal/agentview` package does the fetch+parse once (exec `claude agents --json`, map to typed `Session`s, `ErrUnavailable` on a missing/failing CLI). The nav TUI adds a `screenAgents` reached by `a` from the picker; the WebUI adds a `GET /api/agents` handler + an `agents-updated` SSE broadcast + a Svelte section. Both surfaces consume the same `agentview` package.

**Tech Stack:** Go (stdlib `os/exec`, `encoding/json`, `testing` — table-driven, hand-rolled fakes; NO testify/mockery), Charm bubbletea/lipgloss (TUI), stdlib `net/http` + existing SSE hub (WebUI backend), Svelte + Vite (WebUI frontend). Spec: `docs/superpowers/specs/2026-07-04-nav-agents-view-design.md`.

## Global Constraints

- Data source is exactly `claude agents --json` (subcommand takes **zero** positional args). Verified schema per entry: `pid` (int), `cwd` (string), `kind` (string), `startedAt` (**epoch milliseconds**, int), `sessionId` (string), `name` (string), `status` (string).
- **Read-only.** No attach / steer / kill / launch from this view.
- **Drop "last output line"** — not in the JSON; do NOT read transcript files.
- **Show all sessions**, `kind` labelled per row (no kind filter).
- `claude` missing / non-zero exit → `ErrUnavailable` (surfaces show an "unavailable" state, not a hard error). Empty array `[]` → zero sessions (not an error). Malformed JSON → a distinct parse error (surfaced, not swallowed).
- No new Go modules; no package-level mutable global state; `context.Context` is the first param of I/O funcs. Errors return up — no `os.Exit`/stderr below `main`.
- Gates: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green, and `npm run build` (in `web/`) succeeds so the embedded `dist` stays current.

---

## File Structure

- **Create** `internal/agentview/agentview.go` — the `Session` type, `Runner`/`ExecRunner`, `ErrUnavailable`, `List`. Single responsibility: fetch + parse Claude's agent listing.
- **Create** `internal/agentview/agentview_test.go` — table tests with a fake `Runner`.
- **Create** `internal/nav/agents.go` — nav Agents screen: `agentRow`, `buildAgentRows`, `shortRepo`, `loadAgentsCmd`, `updateAgentsKeys`, `viewAgents`. (Mirrors `internal/nav/overview.go`'s "logic+view for one screen in one file" shape.)
- **Create** `internal/nav/agents_test.go` — `buildAgentRows`/`shortRepo` unit tests, Update-transition tests, golden test.
- **Create** `internal/nav/testdata/agents.golden` — rendered Agents screen (via `-update`).
- **Modify** `internal/nav/types.go` — add `screenAgents` const; add `agentsMsg`/`agentsErrMsg`.
- **Modify** `internal/nav/model.go` — add `agents []agentRow`, `agentsSel int`, `agentsState loadState`, `agentsUnavailable bool` fields.
- **Modify** `internal/nav/update.go` — route `screenAgents` in the key block; add the `a` case in the picker focusList switch; handle `agentsMsg`/`agentsErrMsg`.
- **Modify** `internal/nav/view.go` — add the `screenAgents` branch in `View()`.
- **Create** `internal/api/agents.go` — `AgentsHandler` (`GET /api/agents`).
- **Create** `internal/api/agents_test.go` — handler tests (success + unavailable).
- **Modify** `cmd/bridge/serve.go` — build+register `agentsH`; broadcast `agents-updated` on the ticker.
- **Create** `web/src/lib/stores/agents.js` — store mirroring `overview.js`.
- **Modify** `web/src/App.svelte` — add the Agents section.

---

## Task 1: `internal/agentview` core — fetch + parse

**Files:**
- Create: `internal/agentview/agentview.go`
- Test: `internal/agentview/agentview_test.go`

**Interfaces:**
- Consumes: nothing (new leaf package).
- Produces:
  - `type Session struct { PID int; CWD, Kind, SessionID, Name, Status string; StartedAt time.Time }` with JSON tags `pid,cwd,kind,sessionId,name,status,startedAt`.
  - `type Runner interface { Output(ctx context.Context, name string, args ...string) ([]byte, error) }`
  - `type ExecRunner struct{}` implementing `Runner`.
  - `var ErrUnavailable error`
  - `func List(ctx context.Context, run Runner) ([]Session, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/agentview/agentview_test.go`:

```go
package agentview

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.out, f.err
}

func TestList_ValidArray_ParsesSortsAndConvertsEpoch(t *testing.T) {
	// startedAt is epoch-ms. 1783094237071 = 2026-07-01T... ; exact instant asserted below.
	raw := `[
	  {"pid":2,"cwd":"/home/u/b","kind":"interactive","startedAt":1000,"sessionId":"s-idle","name":"zeta","status":"idle"},
	  {"pid":1,"cwd":"/home/u/a","kind":"background","startedAt":2000,"sessionId":"s-busy","name":"alpha","status":"busy"}
	]`
	got, err := List(context.Background(), fakeRunner{out: []byte(raw)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// busy sorts first, regardless of input order.
	if got[0].Name != "alpha" || got[0].Status != "busy" {
		t.Errorf("row 0 = %q/%q, want alpha/busy", got[0].Name, got[0].Status)
	}
	if got[1].Name != "zeta" {
		t.Errorf("row 1 = %q, want zeta", got[1].Name)
	}
	// epoch-ms → time.Time.
	if !got[0].StartedAt.Equal(time.UnixMilli(2000)) {
		t.Errorf("StartedAt = %v, want %v", got[0].StartedAt, time.UnixMilli(2000))
	}
	if got[0].Kind != "background" || got[0].SessionID != "s-busy" || got[0].PID != 1 {
		t.Errorf("field mapping wrong: %+v", got[0])
	}
}

func TestList_EmptyArray_ReturnsNoSessions(t *testing.T) {
	got, err := List(context.Background(), fakeRunner{out: []byte(`[]`)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestList_RunnerError_IsUnavailable(t *testing.T) {
	_, err := List(context.Background(), fakeRunner{err: errors.New("exec: \"claude\": not found")})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestList_MalformedJSON_IsNotUnavailable(t *testing.T) {
	_, err := List(context.Background(), fakeRunner{out: []byte(`{not json`)})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("malformed JSON should be a distinct parse error, got ErrUnavailable")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentview/ -v`
Expected: FAIL — package/`List`/`ErrUnavailable` undefined (does not compile).

- [ ] **Step 3: Write the implementation**

Create `internal/agentview/agentview.go`:

```go
// Package agentview wraps `claude agents --json`, Claude Code's local listing of
// live agent sessions, into typed values for bridge's nav TUI and WebUI. It is a
// read-only reporter: it never attaches to, steers, or kills a session.
package agentview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"
)

// Session is one live Claude Code session reported by `claude agents --json`.
type Session struct {
	PID       int       `json:"pid"`
	CWD       string    `json:"cwd"`
	Kind      string    `json:"kind"` // "interactive" | "background"
	SessionID string    `json:"sessionId"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // "busy" | "idle" | ...
	StartedAt time.Time `json:"startedAt"`
}

// Runner runs an external command and returns its stdout. The consumer defines it
// so tests inject a fake without a real `claude` binary.
type Runner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the production Runner: it shells out via os/exec.
type ExecRunner struct{}

// Output runs name+args and returns stdout.
func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ErrUnavailable means the claude CLI is absent or `claude agents` failed. Callers
// render an "unavailable" state rather than surfacing a hard error.
var ErrUnavailable = errors.New("claude agent view unavailable")

// dto mirrors the raw JSON entry; startedAt is epoch milliseconds.
type dto struct {
	PID       int    `json:"pid"`
	CWD       string `json:"cwd"`
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"`
}

// List returns the live Claude sessions from `claude agents --json`, sorted
// busy-first then by name for a stable display order. An empty array is a valid
// zero-session result. A run failure (missing binary / non-zero exit) is wrapped as
// ErrUnavailable; malformed JSON returns a distinct parse error.
func List(ctx context.Context, run Runner) ([]Session, error) {
	out, err := run.Output(ctx, "claude", "agents", "--json")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var raw []dto
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse claude agents json: %w", err)
	}
	sessions := make([]Session, 0, len(raw))
	for _, d := range raw {
		sessions = append(sessions, Session{
			PID:       d.PID,
			CWD:       d.CWD,
			Kind:      d.Kind,
			SessionID: d.SessionID,
			Name:      d.Name,
			Status:    d.Status,
			StartedAt: time.UnixMilli(d.StartedAt),
		})
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		bi, bj := sessions[i].Status == "busy", sessions[j].Status == "busy"
		if bi != bj {
			return bi // busy first
		}
		return sessions[i].Name < sessions[j].Name
	})
	return sessions, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agentview/ -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/agentview/ ; go vet ./internal/agentview/
git add internal/agentview/
git commit -m "feat(agentview): wrap claude agents --json into typed sessions

New leaf package: List execs \`claude agents --json\`, maps entries to typed
Session values (epoch-ms startedAt -> time.Time), sorts busy-first then by
name. ErrUnavailable on a missing/failing CLI; malformed JSON is a distinct
parse error; empty array is zero sessions. Runner interface keeps it testable
with a hand-rolled fake.

Refs #170

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01F8iZEXe7G85ANUPEzg1MR1"
```

---

## Task 2: nav TUI — the Agents screen

**Files:**
- Create: `internal/nav/agents.go`
- Create: `internal/nav/agents_test.go`
- Create: `internal/nav/testdata/agents.golden`
- Modify: `internal/nav/types.go` (add `screenAgents`; add `agentsMsg`/`agentsErrMsg`)
- Modify: `internal/nav/model.go` (add agents fields)
- Modify: `internal/nav/update.go` (route screen; picker `a` case; handle msgs)
- Modify: `internal/nav/view.go` (`View()` branch)

**Interfaces:**
- Consumes: `agentview.List`, `agentview.Session`, `agentview.ExecRunner`, `agentview.ErrUnavailable` (Task 1); existing `panel(w, title, body)`, styles `stMuted/stAccent/stOk/stWarn/stTitle`, `selectableLine(bool, string)`, `trunc(string, int)`, `envLabel(string)`, `humanLastAccessed(time.Duration)`, `m.hintLine(string)` (all in `internal/nav`).
- Produces (used only within nav): `screenAgents`, `agentRow`, `buildAgentRows(sessions []agentview.Session, home string, now time.Time) []agentRow`, `shortRepo(cwd, home string) string`, `loadAgentsCmd() tea.Cmd`, `(m Model) updateAgentsKeys(tea.KeyMsg) (Model, tea.Cmd)`, `(m Model) viewAgents() string`.

### Step group A — `shortRepo` + `buildAgentRows` (pure helpers, TDD)

- [ ] **Step A1: Write failing tests**

Create `internal/nav/agents_test.go`:

```go
package nav

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
)

func TestShortRepo(t *testing.T) {
	tests := []struct {
		name, cwd, home, want string
	}{
		{"home prefix", "/home/u/repos/bridge", "/home/u", "~/repos/bridge"},
		{"exact home", "/home/u", "/home/u", "~"},
		{"no home match", "/opt/x", "/home/u", "/opt/x"},
		{"empty home disables", "/home/u/x", "", "/home/u/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortRepo(tt.cwd, tt.home); got != tt.want {
				t.Errorf("shortRepo(%q,%q) = %q, want %q", tt.cwd, tt.home, got, tt.want)
			}
		})
	}
}

func TestBuildAgentRows(t *testing.T) {
	now := time.UnixMilli(100_000)
	sessions := []agentview.Session{
		{Name: "alpha", Status: "busy", Kind: "interactive", CWD: "/home/u/a", StartedAt: time.UnixMilli(40_000)},
		{Name: "zeta", Status: "idle", Kind: "background", CWD: "/opt/z", StartedAt: time.UnixMilli(40_000)},
	}
	rows := buildAgentRows(sessions, "/home/u", now)
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2", len(rows))
	}
	if rows[0].dot != "●" || rows[0].repo != "~/a" || rows[0].kind != "interactive" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].dot != "○" || rows[1].repo != "/opt/z" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	if rows[0].age == "" {
		t.Errorf("age should be populated")
	}
}
```

- [ ] **Step A2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestShortRepo|TestBuildAgentRows' -v`
Expected: FAIL — `shortRepo`/`buildAgentRows`/`agentRow` undefined.

- [ ] **Step A3: Create `internal/nav/agents.go` with the pure helpers**

```go
package nav

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/agentview"
)

// agentRow is one rendered row on the Agents screen.
type agentRow struct {
	dot    string // ● busy · ○ otherwise
	kind   string
	name   string
	status string
	repo   string // shortened cwd
	age    string
}

// shortRepo abbreviates an absolute cwd for display: a leading home prefix
// becomes "~". home == "" disables the substitution.
func shortRepo(cwd, home string) string {
	if home != "" && cwd == home {
		return "~"
	}
	if home != "" && strings.HasPrefix(cwd, home+"/") {
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return cwd
}

// buildAgentRows turns agentview.Session values into display rows: a status dot,
// the shortened repo, and a humanized age relative to now. Pure for testability.
func buildAgentRows(sessions []agentview.Session, home string, now time.Time) []agentRow {
	rows := make([]agentRow, 0, len(sessions))
	for _, s := range sessions {
		dot := "○"
		if s.Status == "busy" {
			dot = "●"
		}
		rows = append(rows, agentRow{
			dot:    dot,
			kind:   s.Kind,
			name:   s.Name,
			status: s.Status,
			repo:   shortRepo(s.CWD, home),
			age:    humanLastAccessed(now.Sub(s.StartedAt)),
		})
	}
	return rows
}
```

- [ ] **Step A4: Run tests**

Run: `go test ./internal/nav/ -run 'TestShortRepo|TestBuildAgentRows' -v`
Expected: PASS.

### Step group B — screen state, load Cmd, Update wiring

- [ ] **Step B1: Add screen const + messages + model fields**

In `internal/nav/types.go`, extend the screen block:

```go
const (
	screenPicker screen = iota
	screenDash
	screenOverview
	screenAgents
)
```

And add to the `// --- messages ---` section:

```go
type agentsMsg struct{ rows []agentRow }
type agentsErrMsg struct {
	err         error
	unavailable bool
}
```

In `internal/nav/model.go`, add fields to the `Model` struct (near the overview fields `overview`/`overviewState`):

```go
	agents            []agentRow
	agentsSel         int
	agentsState       loadState
	agentsUnavailable bool
```

- [ ] **Step B2: Add `loadAgentsCmd` + `updateAgentsKeys` to `agents.go`**

Append to `internal/nav/agents.go`:

```go
// loadAgentsCmd fetches live Claude sessions off the Update loop and returns an
// agentsMsg (or agentsErrMsg on failure, distinguishing "claude unavailable" from
// a real parse error so the view can show the right notice).
func loadAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, err := agentview.List(context.Background(), agentview.ExecRunner{})
		if err != nil {
			return agentsErrMsg{err: err, unavailable: errors.Is(err, agentview.ErrUnavailable)}
		}
		home, _ := os.UserHomeDir()
		return agentsMsg{rows: buildAgentRows(sessions, home, time.Now())}
	}
}

// updateAgentsKeys handles key presses on the Agents screen: navigate, r to
// refresh, esc back to the picker. Read-only (no attach/kill).
func (m Model) updateAgentsKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenPicker
		return m, nil
	case "r":
		m.agentsState = loadPending
		return m, loadAgentsCmd()
	case "up", "k":
		if m.agentsSel > 0 {
			m.agentsSel--
		}
	case "down", "j":
		if m.agentsSel < len(m.agents)-1 {
			m.agentsSel++
		}
	case "g", "home":
		m.agentsSel = 0
	case "G", "end":
		if len(m.agents) > 0 {
			m.agentsSel = len(m.agents) - 1
		}
	}
	return m, nil
}
```

- [ ] **Step B3: Route the screen + handle the messages in `update.go`**

In `internal/nav/update.go`, in the `case tea.KeyMsg:` block (currently at lines ~185-198), add the `screenAgents` route **before** the `screenPicker` check:

```go
		if m.screen == screenAgents {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m.updateAgentsKeys(msg)
		}
```

Immediately after the existing `case overviewErrMsg:` block (ends ~line 183), add:

```go
	case agentsMsg:
		m.agents = msg.rows
		m.agentsState = loadOK
		m.agentsUnavailable = false
		if m.agentsSel >= len(m.agents) {
			m.agentsSel = 0
		}
		return m, nil
	case agentsErrMsg:
		m.agentsState = loadErr
		m.agentsUnavailable = msg.unavailable
		if !msg.unavailable && msg.err != nil {
			m.status = "agents unavailable: " + msg.err.Error()
		}
		return m, nil
```

In `updatePicker`'s **focusList** switch (the `switch msg.String()` at ~line 331, alongside the `case "o":` at ~line 364), add:

```go
		case "a":
			m.screen = screenAgents
			m.agentsState = loadPending
			m.agentsSel = 0
			return m, loadAgentsCmd()
```

- [ ] **Step B4: Write the Update-transition tests**

Append to `internal/nav/agents_test.go`. Add `tea "github.com/charmbracelet/bubbletea"` to the file's import block (grouped with the existing imports). The picker-open test mirrors the exact idiom the neighboring `TestUpdatePicker_*` tests use (`internal/nav/update_test.go:106-121`): `initialModel(Config{})`, set `pickerFocus = focusList`, send a `tea.KeyMsg`, type-assert the result back to `Model`.

```go
func TestAgents_PickerKeyOpensScreen(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m2 := out.(Model)
	if m2.screen != screenAgents {
		t.Fatalf("screen = %v, want screenAgents", m2.screen)
	}
	if m2.agentsState != loadPending {
		t.Errorf("agentsState = %v, want loadPending", m2.agentsState)
	}
	if cmd == nil {
		t.Errorf("expected a load cmd")
	}
}

func TestAgents_MsgPopulatesRows(t *testing.T) {
	m := Model{screen: screenAgents, agentsState: loadPending}
	rows := []agentRow{{name: "a"}, {name: "b"}}
	next, _ := m.Update(agentsMsg{rows: rows})
	m2 := next.(Model)
	if len(m2.agents) != 2 || m2.agentsState != loadOK {
		t.Errorf("agents=%d state=%v", len(m2.agents), m2.agentsState)
	}
}

func TestAgents_ErrUnavailableSetsFlag(t *testing.T) {
	m := Model{screen: screenAgents, agentsState: loadPending}
	next, _ := m.Update(agentsErrMsg{unavailable: true})
	m2 := next.(Model)
	if m2.agentsState != loadErr || !m2.agentsUnavailable {
		t.Errorf("state=%v unavailable=%v", m2.agentsState, m2.agentsUnavailable)
	}
}

func TestAgents_EscReturnsToPicker(t *testing.T) {
	m := Model{screen: screenAgents}
	m2, _ := m.updateAgentsKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.screen != screenPicker {
		t.Errorf("screen = %v, want screenPicker", m2.screen)
	}
}

func TestAgents_DownMovesSelection(t *testing.T) {
	m := Model{screen: screenAgents, agents: []agentRow{{}, {}, {}}}
	m2, _ := m.updateAgentsKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m2.agentsSel != 1 {
		t.Errorf("agentsSel = %d, want 1", m2.agentsSel)
	}
}
```

**Model construction:** `initialModel(Config{})` returns a picker model (screen defaults to `screenPicker`); the tests above set `pickerFocus`/`screen`/fields directly, exactly as the neighboring `TestUpdatePicker_*` and `TestUpdateDash_*` tests do (`internal/nav/update_test.go`). Do NOT invent a construction helper.

- [ ] **Step B5: Run to verify failure, then it won't compile without `viewAgents`**

Run: `go test ./internal/nav/ -run TestAgents -v`
Expected: FAIL to compile — `View()` has no `screenAgents` branch yet is fine (falls through), but `viewAgents` is referenced only after Step group C. If compilation fails solely on missing `viewAgents`, proceed to group C (the render), then run.

### Step group C — render + `View()` branch + golden

- [ ] **Step C1: Add `viewAgents` to `agents.go`**

Append to `internal/nav/agents.go`:

```go
// viewAgents renders the read-only Agents screen: a bordered panel listing every
// live Claude session (status dot, kind, name, status, repo, age), plus loading,
// unavailable, and empty states. Pure — no I/O (age was humanized at load time).
func (m Model) viewAgents() string {
	w := m.width
	title := "bridge · " + envLabel(m.cfg.Environment) + " · Agents"
	if m.agentsState == loadPending {
		return panel(w, title, stMuted.Render("◐ loading claude sessions…"))
	}
	if m.agentsUnavailable {
		body := stWarn.Render("⚠ Claude Agent View unavailable") + "\n" +
			stMuted.Render("is the `claude` CLI installed and on PATH?")
		return panel(w, title, body)
	}
	if len(m.agents) == 0 {
		return panel(w, title, stMuted.Render("No live Claude sessions."))
	}
	var b strings.Builder
	for i, r := range m.agents {
		line := fmt.Sprintf("%s %-11s %-20s %-7s %-28s %s",
			r.dot, trunc(r.kind, 11), trunc(r.name, 20), trunc(r.status, 7), trunc(r.repo, 28), r.age)
		b.WriteString(selectableLine(i == m.agentsSel, line) + "\n")
	}
	sections := []string{
		panel(w, title, strings.TrimRight(b.String(), "\n")),
		m.hintLine("↑↓ move · r refresh · esc back · q quit"),
	}
	return strings.Join(sections, "\n")
}
```

- [ ] **Step C2: Add the `View()` branch**

In `internal/nav/view.go` `View()` (currently lines 57-68), add before the final `return m.viewDash()`:

```go
	if m.screen == screenAgents {
		return m.viewAgents()
	}
```

- [ ] **Step C3: Run the Update/unit tests**

Run: `go test ./internal/nav/ -run 'TestAgents|TestShortRepo|TestBuildAgentRows' -v`
Expected: PASS.

- [ ] **Step C4: Write the golden test**

Append to `internal/nav/agents_test.go`. Use the package's real helpers (both in `navtest_test.go`): `assertGolden(t, name, got)` — which appends `.golden` and honors the shared `var update` `-update` flag — and `stripANSI(string)`, since `assertGolden` compares verbatim (it does NOT strip ANSI itself; the overview golden uses `s.frame()`, which strips). Pass `stripANSI(m.viewAgents())` and the bare name `"agents"` (→ `testdata/agents.golden`):

```go
func TestViewAgents_Golden(t *testing.T) {
	now := time.UnixMilli(3_600_000) // 1h after the sessions below
	sessions := []agentview.Session{
		{Name: "bridge [work]", Status: "busy", Kind: "interactive", CWD: "/home/u/repos/bridge", StartedAt: time.UnixMilli(0)},
		{Name: "notes", Status: "idle", Kind: "background", CWD: "/opt/notes", StartedAt: time.UnixMilli(0)},
	}
	m := Model{
		screen:      screenAgents,
		agentsState: loadOK,
		width:       100,
		height:      30,
		agents:      buildAgentRows(sessions, "/home/u", now),
	}
	assertGolden(t, "agents", stripANSI(m.viewAgents()))
}
```

- [ ] **Step C5: Generate the golden and run**

Run: `go test ./internal/nav/ -run TestViewAgents_Golden -update && go test ./internal/nav/ -run TestViewAgents_Golden -v`
Expected: `testdata/agents.golden` created; test PASS. Open the golden and eyeball it: two rows, busy `bridge [work]` first, `~/repos/bridge` repo column, an age like `1h`, the hint line present.

- [ ] **Step C6: Run the full nav package**

Run: `go test ./internal/nav/`
Expected: green — existing picker/dash/overview goldens unchanged (this task adds a new screen, touches no existing render path; `a` was previously unbound in focusList).

- [ ] **Step C7: Commit**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/agents.go internal/nav/agents_test.go internal/nav/testdata/agents.golden \
        internal/nav/types.go internal/nav/model.go internal/nav/update.go internal/nav/view.go
git commit -m "feat(nav): read-only Agents screen listing live claude sessions

New screenAgents, opened with 'a' from the picker: lists every live Claude
session (status dot, kind, name, status, repo-from-cwd, age) via agentview.
r refreshes, esc returns. Loading, unavailable, and empty states handled.
buildAgentRows/shortRepo are pure and unit-tested; a golden covers the render.

Refs #170

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01F8iZEXe7G85ANUPEzg1MR1"
```

---

## Task 3: WebUI backend — `GET /api/agents` + broadcast

**Files:**
- Create: `internal/api/agents.go`
- Create: `internal/api/agents_test.go`
- Modify: `cmd/bridge/serve.go`

**Interfaces:**
- Consumes: `agentview.List`, `agentview.Session`, `agentview.ExecRunner`, `agentview.ErrUnavailable` (Task 1); existing `writeJSON`/`writeError` (`internal/api/errors.go`); existing `web.Event`, `hub.Broadcast` (`cmd/bridge/serve.go`).
- Produces: `type AgentsHandler struct { List func(ctx context.Context) ([]agentview.Session, error) }` serving `GET /api/agents`; a `/api/agents` route; an `agents-updated` SSE broadcast.

- [ ] **Step 1: Write the failing handler tests**

Create `internal/api/agents_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freaxnx01/bridge/internal/agentview"
)

func TestAgentsHandler_Success(t *testing.T) {
	h := &AgentsHandler{List: func(_ context.Context) ([]agentview.Session, error) {
		return []agentview.Session{{Name: "s1", Status: "busy", Kind: "interactive", CWD: "/x"}}, nil
	}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rr.Code)
	}
	var got []agentview.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "s1" {
		t.Errorf("body = %+v", got)
	}
}

func TestAgentsHandler_Unavailable_ReturnsEmpty200(t *testing.T) {
	h := &AgentsHandler{List: func(_ context.Context) ([]agentview.Session, error) {
		return nil, agentview.ErrUnavailable
	}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rr.Code)
	}
	var got []agentview.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty array, got %+v", got)
	}
}

func TestAgentsHandler_MethodNotAllowed(t *testing.T) {
	h := &AgentsHandler{List: func(_ context.Context) ([]agentview.Session, error) { return nil, nil }}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/agents", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", rr.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run TestAgentsHandler -v`
Expected: FAIL — `AgentsHandler` undefined.

- [ ] **Step 3: Write the handler**

Create `internal/api/agents.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/freaxnx01/bridge/internal/agentview"
)

// AgentsHandler handles GET /api/agents: the live Claude sessions as JSON. When
// the claude CLI is unavailable it returns an empty array (200) so the WebUI shows
// an empty section rather than an error.
type AgentsHandler struct {
	List func(ctx context.Context) ([]agentview.Session, error)
}

func (h *AgentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sessions, err := h.List(r.Context())
	if err != nil {
		if errors.Is(err, agentview.ErrUnavailable) {
			writeJSON(w, []agentview.Session{})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, sessions)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -run TestAgentsHandler -v`
Expected: PASS.

- [ ] **Step 5: Wire it into `serve.go`**

In `cmd/bridge/serve.go`:

1. Ensure the import block includes `"context"` and `"github.com/freaxnx01/bridge/internal/agentview"` (add whichever is missing).
2. Alongside the other handler constructions (near `overviewH`/`reposH`/`captureH`, ~lines 53-82), add:

```go
	agentsH := &api.AgentsHandler{
		List: func(ctx context.Context) ([]agentview.Session, error) {
			return agentview.List(ctx, agentview.ExecRunner{})
		},
	}
```

3. In the `apiMux` registrations (~lines 123-127), add:

```go
	apiMux.Handle("/api/agents", agentsH)
```

4. In the broadcast ticker (~lines 129-141), add a second broadcast in the `case <-t.C:` arm, right after the existing `overview-updated`:

```go
			case <-t.C:
				hub.Broadcast(web.Event{Type: "overview-updated"})
				hub.Broadcast(web.Event{Type: "agents-updated"})
```

- [ ] **Step 6: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean (serve.go compiles with the new handler + imports).

- [ ] **Step 7: Commit**

```bash
gofmt -l internal/api/ cmd/bridge/
git add internal/api/agents.go internal/api/agents_test.go cmd/bridge/serve.go
git commit -m "feat(web): GET /api/agents + agents-updated broadcast

AgentsHandler serves live Claude sessions as JSON (empty array on
ErrUnavailable so the UI degrades gracefully); registered on the api mux and
refreshed via a new agents-updated SSE event on the existing 10s ticker.

Refs #170

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01F8iZEXe7G85ANUPEzg1MR1"
```

---

## Task 4: WebUI frontend — store + Agents section

**Files:**
- Create: `web/src/lib/stores/agents.js`
- Modify: `web/src/App.svelte`

**Interfaces:**
- Consumes: `/api/agents` (Task 3); existing `get` helper (`web/src/lib/api.js`), `sseEvent` store (`web/src/lib/stores/sse.js`). JSON field names from `agentview.Session` tags: `name`, `status`, `kind`, `cwd`, `sessionId`, `pid`, `startedAt`.
- Produces: `agents` writable store + `loadAgents()`; an Agents section in `App.svelte`.

- [ ] **Step 1: Create the store** (mirrors `web/src/lib/stores/overview.js`)

Create `web/src/lib/stores/agents.js`:

```js
import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const agents = writable([])

export async function loadAgents() {
  const data = await apiGet('/api/agents')
  agents.set(data ?? [])
}

sseEvent.subscribe(ev => {
  if (ev?.type === 'agents-updated') loadAgents()
})
```

- [ ] **Step 2: Render the section in `App.svelte`**

Replace `web/src/App.svelte` with:

```svelte
<script>
  import { onMount } from 'svelte';
  import { loadRepos, repos } from './lib/stores/repos.js';
  import { loadAgents, agents } from './lib/stores/agents.js';

  onMount(() => { loadRepos(); loadAgents(); });

  function shortRepo(cwd) {
    if (!cwd) return '';
    const parts = cwd.split('/').filter(Boolean);
    return parts.slice(-2).join('/');
  }
</script>

<main>
  <h1>Bridge WebUI</h1>
  <p>{$repos.length} repos loaded.</p>

  <section>
    <h2>Agents · {$agents.length}</h2>
    {#if $agents.length === 0}
      <p>No live Claude sessions.</p>
    {:else}
      <ul>
        {#each $agents as a}
          <li>
            <strong>{a.name}</strong> · {a.status} · {a.kind} · {shortRepo(a.cwd)}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</main>
```

- [ ] **Step 3: Build the web app**

Run: `cd web && npm install && npm run build`
Expected: build succeeds; `web/dist/` (embedded by `internal/web`) is regenerated. (If `npm install` was already run, `npm run build` alone suffices.)

- [ ] **Step 4: Verify the Go build still embeds cleanly**

Run: `go build ./...`
Expected: clean — the `//go:embed dist` in `internal/web/server.go` picks up the rebuilt assets.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/stores/agents.js web/src/App.svelte web/dist
git commit -m "feat(web): Agents section in the WebUI

New agents store (GET /api/agents, refetch on agents-updated SSE) and an
Agents list in App.svelte showing name/status/kind/repo per live session.

Refs #170

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01F8iZEXe7G85ANUPEzg1MR1"
```

**Note:** if the repo's `.gitignore` excludes `web/dist/**` (it was recently adjusted for the embed), the build artifacts may be intentionally committed or intentionally ignored. Check `git status` after the build: if `web/dist` is ignored, drop it from `git add` and rely on the build step to regenerate it (the embed reads the working-tree `dist`). Match whatever the repo already does for `dist` — do not force-add ignored files.

---

## Task 5: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Go gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # expect empty
go vet ./...                          # expect clean
go test -race ./...                   # expect all pass
```
Expected: the new `agentview`, `nav`, and `api` tests pass under `-race`; nothing else regressed.

- [ ] **Step 2: Lint (if available)**

Run: `golangci-lint run` (or `golangci-lint run ./internal/agentview/... ./internal/nav/... ./internal/api/... ./cmd/bridge/...`).
Expected: clean. If `golangci-lint` is not installed, note it; `go vet` is the gate.

- [ ] **Step 3: Web build**

Run: `cd web && npm run build`
Expected: succeeds (dist regenerated).

- [ ] **Step 4: Manual smoke (best-effort, needs a live `claude`)**

Run:
```bash
just build
bridge nav            # in the picker, focus the repo list, press 'a'
                      #   → Agents screen lists live claude sessions (or "No live
                      #     Claude sessions." / the unavailable notice); r refreshes;
                      #     esc returns to the picker.
bridge serve &        # then open the WebUI; the Agents section lists the same
                      #   sessions and refreshes on the 10s tick.
```
Report what the screen showed. If no `claude` sessions are running, confirm the empty/unavailable states render (not a crash).

- [ ] **Step 5: Report**

Report Steps 1-3 output verbatim and the Step 4 smoke result. No success claims without command output.

---

## Notes for the implementer

- **Mirror, don't invent.** `agentview` mirrors `internal/syncer`'s Runner/ExecRunner idiom; the nav screen mirrors `internal/nav/overview.go` (logic+view in one file, `updateOverviewKeys` shape, `overviewMsg` handling); the api handler mirrors `internal/api/overview.go`; the store mirrors `web/src/lib/stores/overview.js`. When a signature here is ambiguous, match the neighbor.
- **Golden determinism.** `viewAgents` is pure — age is humanized at load time into `agentRow.age`, so the golden never depends on the wall clock. Build the model's `agents` via `buildAgentRows(fixedSessions, fixedHome, fixedNow)` in the test.
- **No styled-string padding.** Columns are padded with `%-Ns` on *plain* strings; the status dot is a fixed leading glyph, never padded-while-styled (this avoids the ANSI-width bug fixed in #157).
- **`a` is currently unbound** in the picker focusList switch (verified: cases are up/k, down/j, home/g, end/G, pgup/ctrl+u, pgdown/ctrl+d, `/`, r, ctrl+n, o, enter). If a rebase introduces an `a` binding, pick another free letter and update the hint text + tests.
- If you hit a blocker, find the fix and note it inline here.

## Sequencing note (legend, #157)

The #157 status-glyph legend (PR #187) is not on `main` yet. This view's `●`/`○`
status dots are legend-worthy. **If #157 has merged** by the time you implement this,
add two entries to `legendEntries` in `internal/nav/view.go` — `{"●", stOk, "agent
session busy", "Agents"}` and `{"○", stMuted, "agent session idle", "Agents"}` — and
extend that test's expected set. **If #157 has not merged**, skip this; note it in the
PR so it's picked up when the branches reconcile.
```
