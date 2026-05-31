# `bridge nav` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new `bridge nav` subcommand — a two-screen interactive terminal navigator (repo picker → per-repo dashboard of tmux sessions + worktrees) that survives attach/detach via `tea.ExecProcess`.

**Architecture:** One Bubble Tea program in a new `internal/nav` package with a `screen` field (`picker` → `dash`). Async data (remote repos, git-dirty) arrives as `tea.Msg`s behind a spinner. Attaching/launching runs through `tea.ExecProcess`, so the same program resumes (refreshed) on detach. `internal/tui` and bare `bridge` are untouched; the only additive change to existing code is a `session_activity` field on `core` sessions and an exported `worktree.List`.

**Tech Stack:** Go, `spf13/cobra`, `charmbracelet/bubbletea` v1.3.10, `bubbles/{spinner,textinput}`, `lipgloss`. Stdlib `testing`, table-driven, hand-rolled fakes (no testify).

**Spec:** [`docs/specs/2026-05-31-bridge-nav-design.md`](../specs/2026-05-31-bridge-nav-design.md) · **Wireframe/flow:** [`docs/design/nav/`](../design/nav/)

**Conventions for every task:** run `gofmt -w` on touched files; final gate is `go test -race ./...`, `go vet ./...`, `golangci-lint run`. Commit messages are Conventional Commits; do not push until the user asks.

---

## File structure

New files:

- `internal/nav/types.go` — enums (`screen`, `focus`), row structs, `Config`, `Msg` types.
- `internal/nav/format.go` — pure helpers: `humanLastAccessed`, `filterRepos`, `sortDashRows`, `buildDashRows`.
- `internal/nav/data.go` — `tea.Cmd` constructors that call `core`/`forge`/`worktree`/`launcher`/`agents`.
- `internal/nav/model.go` — `Model`, `initialModel`, `Init`.
- `internal/nav/update.go` — `Update` (picker + dash + modal reducers).
- `internal/nav/view.go` — styles + `View` (picker, dash, modal).
- `internal/nav/run.go` — `Run(Config)` + non-TTY detection + `tea.ExecProcess` dispatch.
- `internal/nav/*_test.go` — table-driven tests per unit.
- `cmd/bridge/nav.go` — cobra wiring + `--once`; injects `cloneRemoteRepo`.
- `cmd/bridge/nav_test.go` — `--once` smoke.

Modified (additive only):

- `internal/core/session.go` (+ `session_test.go`) — `LastActivity` field, 4-field `ParseTmuxList`, `session_activity` in `LiveSessions` format.
- `internal/worktree/worktree.go` (+ `worktree_test.go`) — exported `Entry` + `List`.
- `README.md`, `CHANGELOG.md`.

---

## Task 1: Add `LastActivity` to core sessions

**Files:**
- Modify: `internal/core/session.go`
- Test: `internal/core/session_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/core/session_test.go`:

```go
func TestParseTmuxList_FourFields_PopulatesLastActivity(t *testing.T) {
	// name|attached|created|activity
	raw := "fix-x|1|1000|1900\ndocs|0|1000|1500\n"
	got, err := ParseTmuxList(raw, 2000)
	if err != nil {
		t.Fatalf("ParseTmuxList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].State != "attached" {
		t.Errorf("session[0].State = %q, want attached", got[0].State)
	}
	if want := time.Unix(1900, 0); !got[0].LastActivity.Equal(want) {
		t.Errorf("session[0].LastActivity = %v, want %v", got[0].LastActivity, want)
	}
	if want := time.Unix(1500, 0); !got[1].LastActivity.Equal(want) {
		t.Errorf("session[1].LastActivity = %v, want %v", got[1].LastActivity, want)
	}
}
```

Check the existing `session_test.go` for any fixture using the **3-field** `name|attached|created` format and update those literals to 4 fields (append `|<activity>`), or the existing tests will fail against the new parser.

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./internal/core -run TestParseTmuxList_FourFields -v`
Expected: FAIL (compile error — `LastActivity` undefined — or `malformed tmux line`).

- [ ] **Step 3: Implement**

In `internal/core/session.go`, add the field to `Session`:

```go
type Session struct {
	SlotID       string        `json:"slot_id"`
	State        string        `json:"state"`
	Age          time.Duration `json:"age"`
	LastActivity time.Time     `json:"last_activity"`
	PID          int           `json:"pid,omitempty"`
	TmuxName     string        `json:"tmux_name"`
}
```

Change the parser to expect 4 fields and fill `LastActivity`:

```go
		parts := strings.Split(line, "|")
		if len(parts) != 4 {
			return nil, fmt.Errorf("malformed tmux line: %q", line)
		}
		attached, _ := strconv.Atoi(parts[1])
		created, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("created: %w", err)
		}
		activity, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("activity: %w", err)
		}
		state := "detached"
		if attached > 0 {
			state = "attached"
		}
		out = append(out, Session{
			SlotID:       parts[0],
			TmuxName:     parts[0],
			State:        state,
			Age:          time.Duration(nowUnix-created) * time.Second,
			LastActivity: time.Unix(activity, 0),
		})
```

And add `#{session_activity}` to the `LiveSessions` format string:

```go
	cmd := exec.Command("tmux", "list-sessions", "-F",
		"#{session_name}|#{session_attached}|#{session_created}|#{session_activity}")
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/core/... -v`
Expected: PASS (including any fixtures you updated to 4 fields).

- [ ] **Step 5: Check other consumers compile**

Run: `go build ./... && grep -rn "ParseTmuxList\|BRIDGE_TMUX_FIXTURE" cmd internal e2e`
Expected: build OK. If any test fixture file (e.g. an e2e `tmux ls` fixture, or `cmd/bridge/sessions_*` tests) feeds 3-field lines, update them to 4 fields. Fix each, re-run `go test ./...`.

- [ ] **Step 6: Commit**

```bash
git add internal/core/session.go internal/core/session_test.go
git commit -m "feat(core): add session LastActivity from tmux session_activity"
```

---

## Task 2: Export `worktree.List`

**Files:**
- Modify: `internal/worktree/worktree.go`
- Test: `internal/worktree/worktree_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/worktree/worktree_test.go` (reuse the existing fake `Runner` in that file; if none exists, define `type fakeRunner struct{ out string; err error }` with a `Run` returning them):

```go
func TestList_ParsesPorcelain_ExcludesPrimary(t *testing.T) {
	out := "worktree /repo\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/fix\nbranch refs/heads/worktree-fix\n\n"
	r := fakeRunner{out: out}
	got, err := List(r, "/repo")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1 (primary excluded)", len(got))
	}
	if got[0].Path != "/repo/.worktrees/fix" || got[0].Branch != "worktree-fix" {
		t.Errorf("entry = %+v, want path=/repo/.worktrees/fix branch=worktree-fix", got[0])
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./internal/worktree -run TestList -v`
Expected: FAIL (`List`/`Entry` undefined).

- [ ] **Step 3: Implement**

In `internal/worktree/worktree.go`, export an `Entry` and a `List`. Reuse the existing `parsePorcelain`:

```go
// Entry is a worktree of a repo: its checkout path and short branch name
// ("" when detached). The primary working tree is excluded by List.
type Entry struct {
	Path   string
	Branch string
}

// List returns the non-primary worktrees of the repo at repoPath, parsed from
// `git worktree list --porcelain`. The primary working tree (repoPath itself)
// is excluded — nav lists isolated worktrees, not the main checkout.
func List(r Runner, repoPath string) ([]Entry, error) {
	out, err := r.Run(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	main := filepath.Clean(repoPath)
	var entries []Entry
	for _, e := range parsePorcelain(out) {
		if filepath.Clean(e.path) == main {
			continue
		}
		entries = append(entries, Entry{Path: e.path, Branch: e.branch})
	}
	return entries, nil
}
```

- [ ] **Step 4: Run test, verify pass**

Run: `go test ./internal/worktree/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): export Entry + List for non-primary worktrees"
```

---

## Task 3: nav types

**Files:**
- Create: `internal/nav/types.go`

- [ ] **Step 1: Create the file**

```go
// Package nav implements `bridge nav`: a two-screen interactive navigator
// (repo picker -> per-repo dashboard of tmux sessions + worktrees). It is a
// single Bubble Tea program that survives attach/detach via tea.ExecProcess.
package nav

import (
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

type screen int

const (
	screenPicker screen = iota
	screenDash
)

type focus int

const (
	focusFilter focus = iota
	focusList
)

type loadState int

const (
	loadPending loadState = iota
	loadOK
	loadErr
)

// repoRow is one picker row. Remote rows carry a forge.RepoRef for clone.
type repoRow struct {
	label  string // display label, e.g. github/public/bridge
	repo   core.Repo
	remote *forge.RepoRef // non-nil => clone-on-select
}

// sessionRow is one global active-session row on the picker.
type sessionRow struct {
	slotID       string
	repoLabel    string
	worktree     string
	agent        string
	state        string // attached | detached
	lastAccessed string
}

// dashRow is one Screen-2 row: a worktree, optionally with a live session.
type dashRow struct {
	worktree     string // basename
	branch       string
	path         string
	agent        string // "" when no live session
	slotID       string // "" when no live session
	state        string // attached | detached | ""
	lastAccessed string // "" when no live session
	hasSession   bool
	dirty        dirtyInfo
	dirtyState   loadState
}

// dirtyInfo is the async git status for a worktree.
type dirtyInfo struct {
	modified int
	ahead    int
	clean    bool
}

// newWorktreeModal is the inline create-worktree input state.
type newWorktreeModal struct {
	name string
	err  string
}

// Config is everything nav needs, injected by cmd/bridge so internal/nav
// stays free of cmd-layer code (e.g. cloneRemoteRepo).
type Config struct {
	ReposRoots   []string
	RemoteCache  string // path to remote.list
	SlotsPath    string // path to slots.json
	DefaultAgent string // BRIDGE_DEFAULT_AGENT ("" => no auto-launch agent; nav uses claude)
	AgentArgs    []string
	Clone        func(ref forge.RepoRef) (core.Repo, error)
	Once         bool
}

// --- messages ---

type reposMsg struct{ rows []repoRow }
type sessionsMsg struct{ rows []sessionRow }
type remoteMsg struct{ rows []repoRow }
type remoteErrMsg struct{ err error }
type dashRowsMsg struct{ rows []dashRow }
type dirtyMsg struct {
	path  string
	info  dirtyInfo
	err   error
}
type cloneDoneMsg struct {
	repo core.Repo
	err  error
}
type wtCreatedMsg struct {
	row dashRow
	err error
}
type execDoneMsg struct{ err error }
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/nav/`
Expected: builds (unused-type warnings are fine; nothing references these yet).

- [ ] **Step 3: Commit**

```bash
git add internal/nav/types.go
git commit -m "feat(nav): add core types, Config, and message shapes"
```

---

## Task 4: Pure formatters (`humanLastAccessed`, `filterRepos`)

**Files:**
- Create: `internal/nav/format.go`
- Test: `internal/nav/format_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package nav

import (
	"testing"
	"time"
)

func TestHumanLastAccessed_TwoUnitsMax(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "0m"},
		{"minutes", 4 * time.Minute, "4m"},
		{"hours-minutes", 3*time.Hour + 12*time.Minute, "3h 12m"},
		{"days-hours", 26*time.Hour + 20*time.Minute, "1d 2h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanLastAccessed(tt.d); got != tt.want {
				t.Errorf("humanLastAccessed(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFilterRepos_CaseInsensitiveSubstring(t *testing.T) {
	rows := []repoRow{
		{label: "github/public/bridge"},
		{label: "github/public/ai-instructions"},
		{label: "gitlab/acme/infra-tools"},
	}
	got := filterRepos(rows, "INFRA")
	if len(got) != 1 || got[0].label != "gitlab/acme/infra-tools" {
		t.Fatalf("filterRepos = %+v, want only infra-tools", got)
	}
	if len(filterRepos(rows, "")) != 3 {
		t.Errorf("empty filter should return all rows")
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run 'TestHumanLastAccessed|TestFilterRepos' -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

```go
package nav

import (
	"fmt"
	"strings"
	"time"
)

// humanLastAccessed renders d as at most two descending units (d/h/m).
// Sub-minute durations render as "0m".
func humanLastAccessed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// filterRepos keeps rows whose label contains q (case-insensitive). Empty q
// returns all rows. Result is a new slice; input is not mutated.
func filterRepos(rows []repoRow, q string) []repoRow {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	needle := strings.ToLower(q)
	out := make([]repoRow, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.label), needle) {
			out = append(out, r)
		}
	}
	return out
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run 'TestHumanLastAccessed|TestFilterRepos' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/format.go internal/nav/format_test.go
git commit -m "feat(nav): add humanLastAccessed + filterRepos helpers"
```

---

## Task 5: `buildDashRows` + `sortDashRows`

**Files:**
- Modify: `internal/nav/format.go`
- Test: `internal/nav/format_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildDashRows_MatchesSessionsAndSorts(t *testing.T) {
	repo := core.Repo{Name: "bridge"}
	wts := []worktree.Entry{
		{Path: "/r/.worktrees/fix-x", Branch: "worktree-fix-x"},
		{Path: "/r/.worktrees/docs", Branch: "worktree-docs"},
		{Path: "/r/.worktrees/spike", Branch: "worktree-spike"},
	}
	slots := []core.Slot{
		{ID: "s-fix", Repo: "bridge", Worktree: "fix-x", Agent: "claude"},
		{ID: "s-docs", Repo: "freaxnx01/bridge", Worktree: "docs", Agent: "copilot"},
	}
	sessions := []core.Session{
		{SlotID: "s-fix", State: "attached", LastActivity: time.Unix(1000, 0)},
		{SlotID: "s-docs", State: "detached", LastActivity: time.Unix(2000, 0)},
	}
	now := time.Unix(3000, 0)
	got := buildDashRows(repo, wts, slots, sessions, now)

	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	// Sessioned rows first, sorted by last-accessed DESC (docs@2000 before fix@1000),
	// then session-less worktrees (spike).
	if got[0].worktree != "docs" || !got[0].hasSession || got[0].agent != "copilot" {
		t.Errorf("row[0] = %+v, want docs/copilot/hasSession", got[0])
	}
	if got[1].worktree != "fix-x" || got[1].state != "attached" {
		t.Errorf("row[1] = %+v, want fix-x/attached", got[1])
	}
	if got[2].worktree != "spike" || got[2].hasSession {
		t.Errorf("row[2] = %+v, want spike with no session", got[2])
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run TestBuildDashRows -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

Add imports `sort`, `"github.com/freaxnx01/bridge/internal/core"`, `"github.com/freaxnx01/bridge/internal/worktree"` to `format.go`, then:

```go
// slotRepoMatches reports whether a slot's Repo field refers to repo. The
// registry stores Repo as either a bare name ("bridge") or an owner-qualified
// label ("freaxnx01/bridge"), so match on equality or a "/"+name suffix.
func slotRepoMatches(slotRepo string, repo core.Repo) bool {
	if strings.EqualFold(slotRepo, repo.Name) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(slotRepo), "/"+strings.ToLower(repo.Name))
}

// buildDashRows joins the repo's worktrees with the global sessions/slots into
// dashboard rows. A worktree gets a live session when a slot for this repo names
// it and that slot's tmux session is live. Rows with a session sort first by
// last-accessed DESC; session-less worktrees follow, name-sorted. dirtyState is
// loadPending (filled later by dirtyMsg).
func buildDashRows(repo core.Repo, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
	liveBySlot := make(map[string]core.Session, len(sessions))
	for _, s := range sessions {
		liveBySlot[s.SlotID] = s
	}
	// worktree name -> slot (for this repo only)
	slotByWt := make(map[string]core.Slot)
	for _, sl := range slots {
		if slotRepoMatches(sl.Repo, repo) && sl.Worktree != "" {
			slotByWt[sl.Worktree] = sl
		}
	}
	rows := make([]dashRow, 0, len(wts))
	for _, e := range wts {
		name := filepath.Base(e.Path)
		row := dashRow{worktree: name, branch: e.Branch, path: e.Path, dirtyState: loadPending}
		if sl, ok := slotByWt[name]; ok {
			if sess, live := liveBySlot[sl.ID]; live {
				row.hasSession = true
				row.slotID = sl.ID
				row.agent = sl.Agent
				row.state = sess.State
				row.lastAccessed = humanLastAccessed(now.Sub(sess.LastActivity))
			}
		}
		rows = append(rows, row)
	}
	sortDashRows(rows, slotByWt, liveBySlot)
	return rows
}

// sortDashRows orders sessioned rows first (last-accessed DESC), then
// session-less rows by worktree name. Uses the live session's LastActivity for
// the time comparison.
func sortDashRows(rows []dashRow, slotByWt map[string]core.Slot, liveBySlot map[string]core.Session) {
	activity := func(r dashRow) (time.Time, bool) {
		if !r.hasSession {
			return time.Time{}, false
		}
		return liveBySlot[r.slotID].LastActivity, true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aok := activity(rows[i])
		bj, bok := activity(rows[j])
		if aok != bok {
			return aok // sessioned before session-less
		}
		if aok && bok {
			return ai.After(bj) // most recent first
		}
		return rows[i].worktree < rows[j].worktree
	})
}
```

Add `"path/filepath"` to the import block.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run TestBuildDashRows -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/format.go internal/nav/format_test.go
git commit -m "feat(nav): build + sort dashboard rows from worktrees and sessions"
```

---

## Task 6: Data Cmds

**Files:**
- Create: `internal/nav/data.go`

These are thin adapters; their pure cores (`buildDashRows`, parsers) are already tested. They are exercised end-to-end by the `--once` smoke + manual TTY run.

- [ ] **Step 1: Create the file**

```go
package nav

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/worktree"
)

func loadLocalReposCmd(roots []string) tea.Cmd {
	return func() tea.Msg {
		var rows []repoRow
		seen := map[string]bool{}
		for _, root := range roots {
			repos, err := core.DiscoverRepos(root)
			if err != nil {
				continue
			}
			for _, r := range repos {
				if seen[r.Path] {
					continue
				}
				seen[r.Path] = true
				rows = append(rows, repoRow{label: repoLabel(r), repo: r})
			}
		}
		return reposMsg{rows: rows}
	}
}

func loadSessionsCmd(slotsPath string) tea.Cmd {
	return func() tea.Msg {
		live, _ := core.LiveSessions()
		slots, _ := core.LoadSlots(slotsPath)
		bySlot := make(map[string]core.Slot, len(slots))
		for _, s := range slots {
			bySlot[s.ID] = s
		}
		now := time.Now()
		rows := make([]sessionRow, 0, len(live))
		for _, s := range live {
			row := sessionRow{
				slotID:       s.SlotID,
				state:        s.State,
				lastAccessed: humanLastAccessed(now.Sub(s.LastActivity)),
			}
			if sl, ok := bySlot[s.SlotID]; ok {
				row.repoLabel = sl.Repo
				row.worktree = sl.Worktree
				row.agent = sl.Agent
			}
			rows = append(rows, row)
		}
		return sessionsMsg{rows: rows}
	}
}

func loadRemoteCmd(cachePath string) tea.Cmd {
	return func() tea.Msg {
		c, err := forge.ReadRepoCache(cachePath)
		if err != nil {
			return remoteErrMsg{err: err}
		}
		rows := make([]repoRow, 0, len(c.Repos))
		for i := range c.Repos {
			ref := c.Repos[i]
			rows = append(rows, repoRow{label: "↓ " + remoteLabel(ref), remote: &ref})
		}
		return remoteMsg{rows: rows}
	}
}

func loadDashRowsCmd(repo core.Repo, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		wts, _ := worktree.List(worktree.ExecRunner{}, repo.Path)
		slots, _ := core.LoadSlots(slotsPath)
		live, _ := core.LiveSessions()
		return dashRowsMsg{rows: buildDashRows(repo, wts, slots, live, time.Now())}
	}
}

// gitDirtyCmd reports modified-file count and ahead count for one worktree.
func gitDirtyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		st, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
		if err != nil {
			return dirtyMsg{path: path, err: err}
		}
		info := dirtyInfo{}
		lines := strings.Split(strings.TrimRight(string(st), "\n"), "\n")
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				info.modified++
			}
		}
		info.clean = info.modified == 0
		if out, err := exec.Command("git", "-C", path, "rev-list", "--count", "@{u}..HEAD").Output(); err == nil {
			info.ahead, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
		return dirtyMsg{path: path, info: info}
	}
}

func createWorktreeCmd(repo core.Repo, name string) tea.Cmd {
	return func() tea.Msg {
		dir, _, err := worktree.Resolve(worktree.ExecRunner{}, repo.Path, name)
		if err != nil {
			return wtCreatedMsg{err: err}
		}
		return wtCreatedMsg{row: dashRow{worktree: name, branch: "worktree-" + name, path: dir, dirtyState: loadPending}}
	}
}

func cloneCmd(clone func(forge.RepoRef) (core.Repo, error), ref forge.RepoRef) tea.Cmd {
	return func() tea.Msg {
		repo, err := clone(ref)
		return cloneDoneMsg{repo: repo, err: err}
	}
}

func repoLabel(r core.Repo) string {
	switch r.Forge {
	case "github":
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		return "github/" + vis + "/" + r.Name
	case "forgejo":
		return "forgejo/" + r.Name
	default:
		if r.Owner != "" {
			return r.Forge + "/" + r.Owner + "/" + r.Name
		}
		return r.Forge + "/" + r.Name
	}
}

func remoteLabel(r forge.RepoRef) string {
	if r.Forge == "github" {
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		return "github/" + vis + "/" + r.Name
	}
	if r.Owner != "" {
		return r.Forge + "/" + r.Owner + "/" + r.Name
	}
	return r.Forge + "/" + r.Name
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/nav/`
Expected: builds.

- [ ] **Step 3: Commit**

```bash
git add internal/nav/data.go
git commit -m "feat(nav): add async data Cmds for repos, sessions, dash, dirty, clone"
```

---

## Task 7: Model + Init

**Files:**
- Create: `internal/nav/model.go`
- Test: `internal/nav/model_test.go`

- [ ] **Step 1: Write the failing test**

```go
package nav

import "testing"

func TestInitialModel_StartsOnPickerFocusFilter(t *testing.T) {
	m := initialModel(Config{ReposRoots: []string{"/tmp"}})
	if m.screen != screenPicker {
		t.Errorf("screen = %d, want screenPicker", m.screen)
	}
	if m.pickerFocus != focusFilter {
		t.Errorf("pickerFocus = %d, want focusFilter", m.pickerFocus)
	}
	if m.remoteState != loadPending {
		t.Errorf("remoteState = %d, want loadPending", m.remoteState)
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run TestInitialModel -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

```go
package nav

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	cfg           Config
	width, height int
	spin          spinner.Model

	screen      screen
	pickerFocus focus

	filter      textinput.Model
	sessions    []sessionRow
	localRepos  []repoRow
	remoteRepos []repoRow
	remoteState loadState
	pickerSel   int

	repo     core.Repo
	dashRows []dashRow
	dashSel  int
	modal    *newWorktreeModal

	status string
}

func initialModel(cfg Config) Model {
	ti := textinput.New()
	ti.Placeholder = "filter…"
	ti.Prompt = "filter: "
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		cfg:         cfg,
		spin:        sp,
		screen:      screenPicker,
		pickerFocus: focusFilter,
		filter:      ti,
		remoteState: loadPending,
		status:      "ready",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spin.Tick,
		loadLocalReposCmd(m.cfg.ReposRoots),
		loadSessionsCmd(m.cfg.SlotsPath),
		loadRemoteCmd(m.cfg.RemoteCache),
	)
}
```

Add `"github.com/freaxnx01/bridge/internal/core"` to the import block (for `core.Repo`).

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run TestInitialModel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/model.go internal/nav/model_test.go
git commit -m "feat(nav): add Model and Init wiring the startup Cmds"
```

---

## Task 8: Update — message reducers

**Files:**
- Create: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package nav

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdate_ReposMsg_PopulatesLocal(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(reposMsg{rows: []repoRow{{label: "a"}, {label: "b"}}})
	got := out.(Model)
	if len(got.localRepos) != 2 {
		t.Fatalf("localRepos = %d, want 2", len(got.localRepos))
	}
}

func TestUpdate_RemoteErrMsg_SetsErrStateKeepsCache(t *testing.T) {
	m := initialModel(Config{})
	m.remoteRepos = []repoRow{{label: "cached"}}
	out, _ := m.Update(remoteErrMsg{err: errFake})
	got := out.(Model)
	if got.remoteState != loadErr {
		t.Errorf("remoteState = %d, want loadErr", got.remoteState)
	}
	if len(got.remoteRepos) != 1 {
		t.Errorf("cached remote rows should survive an error")
	}
}

func TestUpdate_DownFromFilter_MovesFocusToList(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "a"}, {label: "b"}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.pickerFocus != focusList {
		t.Errorf("pickerFocus = %d, want focusList after Down from filter", got.pickerFocus)
	}
}

func TestUpdate_DirtyMsg_FillsRowByPath(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "x", path: "/r/x", dirtyState: loadPending}}
	out, _ := m.Update(dirtyMsg{path: "/r/x", info: dirtyInfo{modified: 3}})
	got := out.(Model)
	if got.dashRows[0].dirtyState != loadOK || got.dashRows[0].dirty.modified != 3 {
		t.Errorf("dirty not applied: %+v", got.dashRows[0])
	}
}

var errFake = fakeErr("boom")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run TestUpdate -v`
Expected: FAIL (`Update` undefined).

- [ ] **Step 3: Implement**

```go
package nav

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case reposMsg:
		m.localRepos = msg.rows
		return m, nil
	case sessionsMsg:
		m.sessions = msg.rows
		return m, nil
	case remoteMsg:
		m.remoteRepos = msg.rows
		m.remoteState = loadOK
		return m, nil
	case remoteErrMsg:
		m.remoteState = loadErr
		m.status = "remote unavailable (cached rows shown)"
		return m, nil

	case dashRowsMsg:
		m.dashRows = msg.rows
		if m.dashSel >= len(m.dashRows) {
			m.dashSel = 0
		}
		return m, m.dirtyCmds()
	case dirtyMsg:
		for i := range m.dashRows {
			if m.dashRows[i].path == msg.path {
				if msg.err != nil {
					m.dashRows[i].dirtyState = loadErr
				} else {
					m.dashRows[i].dirty = msg.info
					m.dashRows[i].dirtyState = loadOK
				}
			}
		}
		return m, nil

	case cloneDoneMsg:
		if msg.err != nil {
			m.status = "clone failed: " + msg.err.Error()
			return m, nil
		}
		return m.enterDash(msg.repo)
	case wtCreatedMsg:
		if msg.err != nil {
			if m.modal != nil {
				m.modal.err = msg.err.Error()
			}
			return m, nil
		}
		m.modal = nil
		return m.launchRow(msg.row)
	case execDoneMsg:
		// Returned from a detached tmux attach/launch: refresh the screen we're on.
		if m.screen == screenDash {
			return m, loadDashRowsCmd(m.repo, m.cfg.SlotsPath)
		}
		return m, loadSessionsCmd(m.cfg.SlotsPath)

	case spinnerTickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg.inner)
		return m, cmd

	case tea.KeyMsg:
		if m.screen == screenPicker {
			return m.updatePicker(msg)
		}
		return m.updateDash(msg)
	}
	// spinner.TickMsg flows here; forward it.
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	return m, cmd
}

// dirtyCmds fires a gitDirty Cmd per dashboard row.
func (m Model) dirtyCmds() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.dashRows))
	for _, r := range m.dashRows {
		cmds = append(cmds, gitDirtyCmd(r.path))
	}
	return tea.Batch(cmds...)
}
```

Note: the `spinnerTickMsg` case above is a simplification — delete it and rely on the trailing `m.spin.Update(msg)` fallthrough, which handles `spinner.TickMsg` directly. (Remove the `case spinnerTickMsg:` block; it is not a real type.)

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run TestUpdate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): add Update message reducers (repos, sessions, dash, dirty, exec)"
```

---

## Task 9: Update — picker & dash key handling + screen transitions

**Files:**
- Modify: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestUpdatePicker_EnterLocalRepo_EntersDash(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	m.localRepos = []repoRow{{label: "github/public/bridge", repo: core.Repo{Name: "bridge", Path: "/r"}}}
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.screen != screenDash {
		t.Fatalf("screen = %d, want screenDash", got.screen)
	}
	if got.repo.Name != "bridge" {
		t.Errorf("repo = %q, want bridge", got.repo.Name)
	}
	if cmd == nil {
		t.Errorf("entering dash should return a loadDashRows Cmd")
	}
}

func TestUpdateDash_N_OpensModal(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := out.(Model)
	if got.modal == nil {
		t.Fatalf("pressing n should open the new-worktree modal")
	}
}

func TestUpdateDash_EscFromDash_ReturnsToPicker(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)
	if got.screen != screenPicker {
		t.Errorf("esc on dash should return to picker, got screen %d", got.screen)
	}
}
```

Add `"github.com/freaxnx01/bridge/internal/core"` to `update_test.go` imports.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run 'TestUpdatePicker|TestUpdateDash' -v`
Expected: FAIL (undefined methods).

- [ ] **Step 3: Implement**

Append to `internal/nav/update.go`:

```go
// visibleRepos is the filtered local+remote row list shown in the picker.
func (m Model) visibleRepos() []repoRow {
	all := append(append([]repoRow{}, m.localRepos...), m.remoteRepos...)
	return filterRepos(all, m.filter.Value())
}

func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.pickerFocus == focusList {
			return m, tea.Quit
		}
		// fallthrough to filter editing for 'q' typed into the filter
	case "esc":
		return m, tea.Quit
	}

	if m.pickerFocus == focusFilter {
		switch msg.Type {
		case tea.KeyDown:
			m.pickerFocus = focusList
			m.filter.Blur()
			m.pickerSel = 0
			return m, nil
		case tea.KeyEnter:
			m.pickerFocus = focusList
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}

	// focusList
	rows := m.visibleRepos()
	switch msg.String() {
	case "up", "k":
		if len(rows) > 0 {
			m.pickerSel = (m.pickerSel + len(rows) - 1) % len(rows)
		}
	case "down", "j":
		if len(rows) > 0 {
			m.pickerSel = (m.pickerSel + 1) % len(rows)
		}
	case "/":
		m.pickerFocus = focusFilter
		m.filter.Focus()
	case "r":
		m.remoteState = loadPending
		return m, loadRemoteCmd(m.cfg.RemoteCache)
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		sel := rows[m.pickerSel]
		if sel.remote != nil {
			m.status = "cloning " + sel.label + "…"
			return m, cloneCmd(m.cfg.Clone, *sel.remote)
		}
		return m.enterDash(sel.repo)
	}
	return m, nil
}

func (m Model) enterDash(repo core.Repo) (tea.Model, tea.Cmd) {
	m.screen = screenDash
	m.repo = repo
	m.dashSel = 0
	m.dashRows = nil
	m.status = "ready"
	return m, loadDashRowsCmd(repo, m.cfg.SlotsPath)
}

func (m Model) updateDash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Modal captures keys first.
	if m.modal != nil {
		return m.updateModal(msg)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.screen = screenPicker
		m.pickerFocus = focusList
		return m, loadSessionsCmd(m.cfg.SlotsPath)
	case "up", "k":
		if n := len(m.dashRows) + 1; n > 0 { // +1 for the "+ create" row
			m.dashSel = (m.dashSel + n - 1) % n
		}
	case "down", "j":
		if n := len(m.dashRows) + 1; n > 0 {
			m.dashSel = (m.dashSel + 1) % n
		}
	case "n":
		m.modal = &newWorktreeModal{}
		return m, nil
	case "enter":
		// The last selectable index is the "+ create" row.
		if m.dashSel == len(m.dashRows) {
			m.modal = &newWorktreeModal{}
			return m, nil
		}
		if m.dashSel < len(m.dashRows) {
			return m.launchRow(m.dashRows[m.dashSel])
		}
	}
	return m, nil
}

func (m Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.modal = nil
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.modal.name)
		if name == "" {
			m.modal.err = "name required"
			return m, nil
		}
		return m, createWorktreeCmd(m.repo, name)
	case tea.KeyBackspace:
		if n := len(m.modal.name); n > 0 {
			m.modal.name = m.modal.name[:n-1]
		}
		return m, nil
	case tea.KeyRunes:
		m.modal.name += string(msg.Runes)
		return m, nil
	}
	return m, nil
}
```

Add `"strings"` to the `update.go` import block.

`launchRow` builds the launch/attach argv and runs it through `tea.ExecProcess` (implemented next task). Add a temporary stub so this task compiles and its tests pass:

```go
// launchRow is completed in Task 10 (tea.ExecProcess wiring). Temporary stub.
func (m Model) launchRow(row dashRow) (tea.Model, tea.Cmd) {
	return m, nil
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run 'TestUpdatePicker|TestUpdateDash' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): picker/dash key handling, screen transitions, create modal"
```

---

## Task 10: `launchRow` via `tea.ExecProcess`

**Files:**
- Modify: `internal/nav/update.go`
- Test: `internal/nav/launch_test.go`

The argv build is pure and tested; `tea.ExecProcess` itself is exercised manually (needs a TTY).

- [ ] **Step 1: Write the failing test**

```go
package nav

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestLaunchArgvFor_AttachExisting(t *testing.T) {
	m := initialModel(Config{})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{worktree: "fix", path: "/r/.worktrees/fix", hasSession: true, slotID: "s-fix"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	want := []string{"tmux", "attach-session", "-t", "s-fix"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", argv, want)
		}
	}
}

func TestLaunchArgvFor_NoSession_LaunchesAgentInWorktree(t *testing.T) {
	m := initialModel(Config{DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{worktree: "fix", path: "/r/.worktrees/fix"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	// tmux new-session -A -s <slot> -c <dir> claude ...
	if argv[0] != "tmux" || argv[1] != "new-session" {
		t.Fatalf("argv = %v, want a tmux new-session launch", argv)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "/r/.worktrees/fix") || !strings.Contains(joined, "claude") {
		t.Errorf("argv = %v, want dir + claude", argv)
	}
}
```

Add `"strings"` import to `launch_test.go`.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run TestLaunchArgvFor -v`
Expected: FAIL (`launchArgvFor` undefined).

- [ ] **Step 3: Implement**

Replace the `launchRow` stub in `update.go` and add `launchArgvFor`. Add imports `"os"`, `"os/exec"`, and the `agents`/`launcher` packages:

```go
// slotIDFor derives the tmux slot/session name for a worktree row: the repo
// name + worktree, filtered to tmux-safe characters.
func (m Model) slotIDFor(row dashRow) string {
	base := m.repo.Name
	if row.worktree != "" {
		base = m.repo.Name + "-" + row.worktree
	}
	return tmuxSafe(base)
}

// launchArgvFor returns the argv to attach an existing session, or to create +
// launch the default agent in a session-less worktree. Honours $TMUX (nested
// switch-client) the same way the open path does.
func (m Model) launchArgvFor(row dashRow) ([]string, error) {
	l := launcher.New()
	if row.hasSession && row.slotID != "" {
		return l.AttachArgv(row.slotID), nil
	}
	name := m.cfg.DefaultAgent
	if name == "" {
		name = "claude"
	}
	spec, err := agents.Resolve(name)
	if err != nil {
		return nil, err
	}
	if len(m.cfg.AgentArgs) > 0 {
		spec.Args = append(append([]string{}, spec.Args...), m.cfg.AgentArgs...)
	}
	slot := m.slotIDFor(row)
	if os.Getenv("TMUX") != "" {
		return l.LaunchArgvNested(slot, row.path, spec)
	}
	return l.LaunchArgv(slot, row.path, spec)
}

func (m Model) launchRow(row dashRow) (tea.Model, tea.Cmd) {
	argv, err := m.launchArgvFor(row)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	c := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
}

// tmuxSafe drops characters tmux session names disallow; empty -> "repo".
func tmuxSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "repo"
	}
	return b.String()
}
```

Add to the import block: `"github.com/freaxnx01/bridge/internal/agents"`, `"github.com/freaxnx01/bridge/internal/launcher"`.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run TestLaunchArgvFor -v && go test ./internal/nav/...`
Expected: PASS (full nav package green).

- [ ] **Step 5: Commit**

```bash
git add internal/nav/update.go internal/nav/launch_test.go
git commit -m "feat(nav): launch/attach rows via tea.ExecProcess with refresh on detach"
```

---

## Task 11: View

**Files:**
- Create: `internal/nav/view.go`
- Test: `internal/nav/view_test.go`

- [ ] **Step 1: Write the failing test**

```go
package nav

import (
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestView_Picker_ShowsFilterAndRepos(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.localRepos = []repoRow{{label: "github/public/bridge"}}
	out := m.View()
	if !strings.Contains(out, "filter:") {
		t.Errorf("picker view missing filter field")
	}
	if !strings.Contains(out, "bridge") {
		t.Errorf("picker view missing repo row")
	}
}

func TestView_Dash_ShowsCreateRowAndRepoName(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x", hasSession: true, agent: "claude", lastAccessed: "1d 2h"}}
	out := m.View()
	if !strings.Contains(out, "fix-x") || !strings.Contains(out, "Create new worktree") {
		t.Errorf("dash view missing rows or create action:\n%s", out)
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run TestView -v`
Expected: FAIL (`View` undefined).

- [ ] **Step 3: Implement**

Create `internal/nav/view.go`. Reuse the btop palette (mirror the colours from `internal/tui/tui.go`). Provide `View()` dispatching on `m.screen`, plus `viewPicker`, `viewDash`, `viewModal`. Keep it logic-free (no I/O):

```go
package nav

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colAccent = lipgloss.Color("#7aa2f7")
	colMuted  = lipgloss.Color("#6b7280")
	colText   = lipgloss.Color("#c0caf5")
	colOk     = lipgloss.Color("#9ece6a")
	colWarn   = lipgloss.Color("#e0af68")
	colBad    = lipgloss.Color("#f7768e")
	colBorder = lipgloss.Color("#3b3b58")
	colSelBg  = lipgloss.Color("#2a2b3d")

	stTitle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true)
	stMuted  = lipgloss.NewStyle().Foreground(colMuted)
	stText   = lipgloss.NewStyle().Foreground(colText)
	stAccent = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stSel    = lipgloss.NewStyle().Background(colSelBg).Foreground(colText)
	stOk     = lipgloss.NewStyle().Foreground(colOk)
	stWarn   = lipgloss.NewStyle().Foreground(colWarn)
	stBad    = lipgloss.NewStyle().Foreground(colBad)
)

func panel(w int, title, body string) string {
	head := stTitle.Render(" " + title + " ")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(w - 2).
		Render(head + "\n\n" + body)
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initialising…"
	}
	if m.screen == screenPicker {
		return m.viewPicker()
	}
	return m.viewDash()
}

func (m Model) viewPicker() string {
	w := m.width
	var sections []string

	if len(m.sessions) > 0 {
		var b strings.Builder
		for _, s := range m.sessions {
			dot := stMuted.Render("○")
			if s.state == "attached" {
				dot = stOk.Render("●")
			}
			b.WriteString(fmt.Sprintf("%s %-24s %-16s %-8s %s\n",
				dot, trunc(s.repoLabel, 24), trunc(s.worktree, 16), s.agent, s.lastAccessed))
		}
		sections = append(sections, panel(w, "Active sessions", strings.TrimRight(b.String(), "\n")))
	}

	title := "Repos"
	switch m.remoteState {
	case loadPending:
		title = "Repos   " + m.spin.View() + " loading remote…"
	case loadErr:
		title = "Repos   " + stWarn.Render("remote unavailable (cached rows shown)")
	}
	var rb strings.Builder
	rb.WriteString(m.filter.View() + "\n\n")
	rows := m.visibleRepos()
	if len(rows) == 0 {
		rb.WriteString(stMuted.Render("no repos — remote rows appear once loaded"))
	}
	for i, r := range rows {
		marker := "  "
		line := "  " + r.label
		if m.pickerFocus == focusList && i == m.pickerSel {
			marker = stAccent.Render("▸ ")
			line = stSel.Render(marker + r.label)
		} else {
			line = marker + stText.Render(r.label)
		}
		rb.WriteString(line + "\n")
	}
	sections = append(sections, panel(w, title, strings.TrimRight(rb.String(), "\n")))

	hint := stMuted.Render("↑↓ move · ⏎ open/attach · / filter · r refresh remote · q quit")
	sections = append(sections, hint)
	return strings.Join(sections, "\n")
}

func (m Model) viewDash() string {
	w := m.width
	header := panel(w, "bridge nav · "+m.repo.Name, stMuted.Render(m.repo.Path))

	var b strings.Builder
	for i, r := range m.dashRows {
		dot := stMuted.Render("·")
		switch r.state {
		case "attached":
			dot = stOk.Render("●")
		case "detached":
			dot = stMuted.Render("○")
		}
		agent := r.agent
		if agent == "" {
			agent = "—"
		}
		la := r.lastAccessed
		if !r.hasSession {
			la = "(no session)"
		}
		line := fmt.Sprintf("%s %-18s %-14s %-8s %-12s %s",
			dot, trunc(r.worktree, 18), trunc(r.branch, 14), agent, la, m.dirtyView(r))
		if i == m.dashSel {
			line = stSel.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	createLine := "  + Create new worktree…"
	if m.dashSel == len(m.dashRows) {
		createLine = stSel.Render(stAccent.Render("▸ ") + "+ Create new worktree…")
	}
	b.WriteString(createLine)

	body := panel(w, "Sessions & Worktrees", strings.TrimRight(b.String(), "\n"))
	hint := stMuted.Render("↑↓ move · ⏎ attach/launch · n new worktree · esc back · q quit")
	footer := stMuted.Render("(later: Branches · Recent commits · Git status · Open issues · forge statusbar)")

	out := header + "\n" + body + "\n" + hint + "\n" + footer
	if m.modal != nil {
		out += "\n" + m.viewModal()
	}
	return out
}

func (m Model) dirtyView(r dashRow) string {
	switch r.dirtyState {
	case loadPending:
		return m.spin.View()
	case loadErr:
		return stMuted.Render("?")
	}
	if r.dirty.clean {
		return stOk.Render("✓ clean")
	}
	s := stBad.Render(fmt.Sprintf("●%d", r.dirty.modified))
	if r.dirty.ahead > 0 {
		s += " " + stWarn.Render(fmt.Sprintf("↑%d", r.dirty.ahead))
	}
	return s
}

func (m Model) viewModal() string {
	body := "name: " + m.modal.name + "_\n\n" +
		stMuted.Render("→ creates worktree-"+m.modal.name+" then launches the default agent") + "\n" +
		stMuted.Render("⏎ create & launch    esc cancel")
	if m.modal.err != "" {
		body += "\n" + stBad.Render(m.modal.err)
	}
	return panel(m.width, "New worktree", body)
}

func trunc(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav -run TestView -v && go test ./internal/nav/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "feat(nav): render picker, dashboard, and create-worktree modal views"
```

---

## Task 12: `Run` + non-TTY fallback + `--once`

**Files:**
- Create: `internal/nav/run.go`
- Test: `internal/nav/run_test.go`

- [ ] **Step 1: Write the failing test**

```go
package nav

import (
	"strings"
	"testing"
)

func TestRun_Once_RendersFrameNoTTY(t *testing.T) {
	// --once must produce output without a TTY (CI smoke path).
	var sb strings.Builder
	err := runOnce(Config{ReposRoots: []string{t.TempDir()}}, &sb)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !strings.Contains(sb.String(), "filter:") {
		t.Errorf("once frame missing picker content:\n%s", sb.String())
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/nav -run TestRun_Once -v`
Expected: FAIL (`runOnce` undefined).

- [ ] **Step 3: Implement**

```go
package nav

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run launches the nav TUI. With cfg.Once it renders one frame and returns
// (smoke path). Otherwise it requires an interactive TTY; without one it prints
// a notice and returns nil rather than starting the program.
func Run(cfg Config) error {
	if cfg.Once {
		return runOnce(cfg, os.Stdout)
	}
	if !isInteractive() {
		fmt.Fprintln(os.Stderr, "bridge nav: needs an interactive terminal (tmux attach is unavailable here)")
		return nil
	}
	p := tea.NewProgram(initialModel(cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func runOnce(cfg Config, w io.Writer) error {
	m := initialModel(cfg)
	m.width, m.height = 130, 42
	// Resolve the synchronous loaders so the frame has content.
	if msg := loadLocalReposCmd(cfg.ReposRoots)(); msg != nil {
		mm, _ := m.Update(msg)
		m = mm.(Model)
	}
	fmt.Fprintln(w, m.View())
	return nil
}

// isInteractive reports whether stdin is a character device (a terminal).
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/nav/...`
Expected: PASS (whole package).

- [ ] **Step 5: Commit**

```bash
git add internal/nav/run.go internal/nav/run_test.go
git commit -m "feat(nav): Run with --once smoke render and non-TTY fallback"
```

---

## Task 13: cobra wiring (`bridge nav`)

**Files:**
- Create: `cmd/bridge/nav.go`
- Test: `cmd/bridge/nav_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNavCmd_Once_RendersFrame(t *testing.T) {
	t.Setenv("BRIDGE_REPOS_ROOT", t.TempDir())
	cmd := newRootCmd() // if the repo builds rootCmd via a constructor; else call navCmd.RunE directly
	cmd.SetArgs([]string{"nav", "--once"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("nav --once: %v", err)
	}
	if !strings.Contains(out.String(), "filter:") {
		t.Errorf("nav --once produced no picker frame:\n%s", out.String())
	}
}
```

If the codebase uses a package-level `rootCmd` (it does — see `cmd/bridge/root.go`) rather than a `newRootCmd()` constructor, adapt: call `rootCmd.SetArgs(...)` / `rootCmd.Execute()` and reset args in a `t.Cleanup`, mirroring the pattern in `cmd/bridge/tui_test.go`. Use whichever the neighbouring tests use.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./cmd/bridge -run TestNavCmd_Once -v`
Expected: FAIL (`nav` command unknown).

- [ ] **Step 3: Implement**

Create `cmd/bridge/nav.go`. Mirror `tui.go`'s wiring; build the `nav.Config`, injecting `cloneRemoteRepo` via an adapter that returns a `core.Repo`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/nav"
)

var navOnce bool

var navCmd = &cobra.Command{
	Use:   "nav",
	Short: "Interactive navigator: pick a repo, then manage its sessions & worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := nav.Config{
			ReposRoots:   reposRoots(),
			RemoteCache:  filepath.Join(cacheRoot(), "remote.list"),
			SlotsPath:    filepath.Join(cacheRoot(), "slots.json"),
			DefaultAgent: os.Getenv("BRIDGE_DEFAULT_AGENT"),
			AgentArgs:    splitAgentArgs(os.Getenv("BRIDGE_DEFAULT_AGENT_ARGS")),
			Once:         navOnce,
			Clone: func(ref forge.RepoRef) (core.Repo, error) {
				dir, err := cloneRemoteRepo(ref)
				if err != nil {
					return core.Repo{}, err
				}
				return repoFromClonedRef(reposRoot(), ref, dir), nil
			},
		}
		return nav.Run(cfg)
	},
}

func init() {
	navCmd.Flags().BoolVar(&navOnce, "once", false, "render one frame to stdout and exit (smoke test, no TTY)")
	rootCmd.AddCommand(navCmd)
}

// splitAgentArgs splits a BRIDGE_DEFAULT_AGENT_ARGS string into argv on
// whitespace. Empty input yields nil.
func splitAgentArgs(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil
	}
	return f
}
```

If a `splitAgentArgs`-equivalent already exists in `cmd/bridge` (check `positional.go`/`preflight.go` for how `BRIDGE_DEFAULT_AGENT_ARGS` is parsed today), reuse it instead of adding a duplicate, to stay DRY.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./cmd/bridge -run TestNavCmd_Once -v`
Expected: PASS.

- [ ] **Step 5: Full suite + gates**

Run:
```bash
gofmt -l internal/nav cmd/bridge
go vet ./...
go test -race ./...
golangci-lint run
```
Expected: `gofmt -l` empty, vet clean, all tests pass under `-race`, lint clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/nav.go cmd/bridge/nav_test.go
git commit -m "feat(cmd): wire \`bridge nav\` subcommand, inject clone adapter"
```

---

## Task 14: Manual TTY verification

No code; verify the interactive paths a `--once` test can't.

- [ ] **Step 1: Build + install**

Run: `just build && bridge --version`

- [ ] **Step 2: Picker**

Run `bridge nav`. Verify: local repos appear immediately; the `Repos` title shows the spinner then resolves; **↓** from the filter moves into the list; typing filters; `r` refreshes remote; active sessions (if any) show at top sorted by last-accessed.

- [ ] **Step 3: Dash + attach-return**

Select a repo with an existing worktree session → dashboard lists it with a git-dirty indicator. Press `⏎` to attach; detach (`Ctrl-b d`) → you return to the **same dashboard**, refreshed (last-accessed updated).

- [ ] **Step 4: Create worktree**

Press `n`, type a name, `⏎`. Verify a `worktree-<name>` worktree is created under `.worktrees/<name>`, the default agent launches and attaches; detach → the new row is listed. Re-trigger with an existing name → inline error, input stays focused.

- [ ] **Step 5: Remote clone-on-select**

With remote rows present (`bridge list -r --refresh` first to warm the cache), select a `↓ remote` row → it clones, then the dashboard for the cloned repo opens.

- [ ] **Step 6: Non-TTY**

Run `echo | bridge nav` (no tty on stdin) → prints the "needs an interactive terminal" notice and exits 0.

---

## Task 15: Docs

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: README** — add to the CLI surface block:

```
bridge nav                      # interactive navigator: pick a repo → dashboard of its sessions & worktrees
```

And a short paragraph describing the two screens + that it returns to the dashboard on detach. Note it's Unix/tmux-only (Windows prints a notice).

- [ ] **Step 2: CHANGELOG** — under `[Unreleased]` → `Added`:

```
- `bridge nav`: interactive two-screen navigator — repo picker (local + async remote, clone-on-select) and a per-repo dashboard of tmux sessions + worktrees with async git-dirty; attach/launch via tmux and return to the dashboard on detach. New `internal/nav` package; `internal/tui` and bare `bridge` unchanged.
```

- [ ] **Step 3: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs(nav): document \`bridge nav\` in README + CHANGELOG"
```

---

## Self-review (done while writing)

- **Spec coverage:** picker local+async-remote+sessions (T6,9,11), clone-on-select (T6,9,13), dashboard rows+sort (T5,11), git-dirty async (T6,8,11), last-accessed (T1,4), attach-then-return (T10), create-worktree+collision (T6,9), non-TTY fallback (T12), untouched existing code (additive-only T1,T2), tests/gates (every task + T13.5). All spec sections map to a task.
- **Type consistency:** `dashRow`/`repoRow`/`sessionRow`/`dirtyInfo`/`Config`/all `*Msg` defined once in T3 and used unchanged after; `launchArgvFor`/`launchRow`/`buildDashRows`/`sortDashRows`/`filterRepos`/`humanLastAccessed` names consistent across tasks.
- **Placeholder scan:** no TBD/TODO; the one stub (`launchRow` in T9) is explicitly completed in T10. The bogus `spinnerTickMsg` case in T8 is called out for removal in the same step.
- **DRY note:** T13 flags reusing any existing `BRIDGE_DEFAULT_AGENT_ARGS` splitter rather than duplicating.
```
