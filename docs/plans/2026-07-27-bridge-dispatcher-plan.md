# bridge dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `bridge dispatch` — a deterministic nightly scheduler that picks eligible GitHub issues across all local repos in priority order, bounded by WIP caps, and applies the `ai-implement` label so the agent-workflow pipeline builds them unattended.

**Architecture:** A new `internal/dispatch` package holds pure decision logic (`Eligible`, `Order`, `ApplyCaps`, `ClassifyFailure`) over plain structs with no I/O, so the bulk of the behaviour is table-testable without a network. `internal/forge` gains milestone, created-at and pull-request reads. `cmd/bridge/dispatch.go` wires cobra subcommands and is the only layer that touches the network, the clock, or the filesystem.

**Tech Stack:** Go 1.25, cobra, stdlib `encoding/json`, `net/http/httptest` for forge tests. No new module dependencies.

**Spec:** [`docs/specs/2026-07-27-bridge-dispatcher-design.md`](../specs/2026-07-27-bridge-dispatcher-design.md)

## Global Constraints

- **No new Go module dependencies.** Config is JSON via stdlib `encoding/json`.
- **GitHub only.** `ai-implement` runs on GitHub Actions; Forgejo/GitLab/ADO repos are skipped by the dispatcher, not errored on.
- **bridge never decides which model runs.** The only label the dispatcher writes for dispatch is `ai-implement`. It must never write `agent:*` or `model:*`.
- **No LLM calls inside bridge.**
- **Pure logic separated from I/O.** Everything in `internal/dispatch` except `Run` takes plain structs and returns plain structs — no `context.Context`, no `forge.Client`, no clock reads.
- **Label vocabulary, exact strings:** `needs-enrichment`, `🧊 parked`, `ai-implement`, `attempt:N` (N is a decimal integer), `failed:<bucket>`, `size:s` / `size:m` / `size:l`.
- **Transient failure buckets:** `api_auth`, `rate_limit`, `infra`. **Substantive:** `max_turns`, `gate_failed`, `no_diff`.
- **Attempt budget:** 2. At `attempt:2` an issue is parked.
- **Every read subcommand supports `--json`** via the existing `emitJSON` helper in `cmd/bridge/output.go`.
- **All timestamps and "now" values are injected as parameters**, never read from `time.Now()` inside `internal/dispatch`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/forge/client.go` | *Modify* — add `Milestone`, `Created` to `Issue`; add `Milestone` and `PullRequest` types |
| `internal/forge/github.go` | *Modify* — parse milestone/created in `ListOpenIssues`; add `ListOpenMilestones`, `ListOpenPullRequests`, `AddLabels` already exists, add `RemoveLabel` |
| `internal/dispatch/types.go` | Create — `Config`, `Limits`, `Schedule`, `State` |
| `internal/dispatch/eligible.go` | Create — `Eligible`, `Attempts`, `ActiveMilestone`, `ClosesIssue`, `HasOpenPR`, the label constants |
| `internal/dispatch/order.go` | Create — `Candidate`, `Order` and its tiebreak helpers |
| `internal/dispatch/caps.go` | Create — `Decision`, `ApplyCaps` |
| `internal/dispatch/failure.go` | Create — `IsTransient`, `NextAction` |
| `internal/dispatch/config.go` | Create — `LoadConfig`, `DefaultConfig`, `LimitFor` |
| `internal/dispatch/state.go` | Create — `ReadState`, `WriteState` (pause flag, last tick, per-night counter) |
| `cmd/bridge/dispatch.go` | Create — cobra wiring, forge I/O, rendering |

Tasks 2–7 are independent of each other once Task 1 lands; Task 8 consumes all of them.

---

### Task 1: Extend `internal/forge` with milestones, created-at, and PRs

**Files:**
- Modify: `internal/forge/client.go:38-47` (the `Issue` struct)
- Modify: `internal/forge/github.go:290-301` (`ghIssue`), `internal/forge/github.go:447-472` (`ListOpenIssues`)
- Test: `internal/forge/github_test.go`

**Interfaces:**
- Consumes: nothing (first task)
- Produces:
  - `forge.Issue` gains `Milestone string` (milestone title, `""` when unset) and `Created time.Time`
  - `type forge.Milestone struct { Title string; DueOn time.Time; Number int }`
  - `type forge.PullRequest struct { Number int; Title string; Body string; Draft bool }`
  - `func (c *GithubClient) ListOpenMilestones(ctx context.Context, owner, repo string) ([]Milestone, error)`
  - `func (c *GithubClient) ListOpenPullRequests(ctx context.Context, owner, repo string) ([]PullRequest, error)`
  - `func (c *GithubClient) RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error`

- [ ] **Step 1: Write the failing test for issue milestone + created parsing**

Add to `internal/forge/github_test.go`:

```go
func TestGithubListOpenIssuesMilestoneAndCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":41,"title":"authors filter","html_url":"u1","labels":[{"name":"feat"}],
		   "updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z",
		   "milestone":{"title":"v2 search","number":3,"due_on":"2026-08-15T00:00:00Z"}},
		  {"number":42,"title":"no milestone","html_url":"u2","labels":[],
		   "updated_at":"2026-07-02T00:00:00Z","created_at":"2026-06-02T00:00:00Z"}
		]`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	issues, err := c.ListOpenIssues(context.Background(), "freaxnx01", "quotes")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues", len(issues))
	}
	if issues[0].Milestone != "v2 search" {
		t.Errorf("milestone: %q", issues[0].Milestone)
	}
	if issues[0].Created.Format("2006-01-02") != "2026-06-01" {
		t.Errorf("created: %v", issues[0].Created)
	}
	// A missing milestone must be the empty string, not a panic.
	if issues[1].Milestone != "" {
		t.Errorf("milestone should be empty, got %q", issues[1].Milestone)
	}
}
```

- [ ] **Step 2: Run it and verify it fails**

Run: `go test ./internal/forge/ -run TestGithubListOpenIssuesMilestoneAndCreated -v`
Expected: FAIL — `issues[0].Milestone undefined (type Issue has no field or method Milestone)`

- [ ] **Step 3: Add the fields to `Issue`**

In `internal/forge/client.go`, replace the `Issue` struct:

```go
type Issue struct {
	Forge     string    `json:"forge"`
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	Milestone string    `json:"milestone,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
	Created   time.Time `json:"created,omitempty"`
}
```

- [ ] **Step 4: Parse them in the GitHub client**

In `internal/forge/github.go`, extend `ghIssue`:

```go
type ghIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Milestone *struct {
		Title  string    `json:"title"`
		Number int       `json:"number"`
		DueOn  time.Time `json:"due_on"`
	} `json:"milestone"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedAt   time.Time `json:"created_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}
```

In `ListOpenIssues`, after building `labels`, add before the `append`:

```go
		milestone := ""
		if i.Milestone != nil {
			milestone = i.Milestone.Title
		}
```

and extend the appended literal with `Milestone: milestone,` and `Created: i.CreatedAt,`.

- [ ] **Step 5: Run the test and verify it passes**

Run: `go test ./internal/forge/ -run TestGithubListOpenIssuesMilestoneAndCreated -v`
Expected: PASS

- [ ] **Step 6: Write the failing test for milestones and PRs**

```go
func TestGithubListOpenMilestones(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state: %q", r.URL.Query().Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":3,"title":"v2 search","due_on":"2026-08-15T00:00:00Z"},
		  {"number":4,"title":"someday","due_on":null}
		]`))
	}))
	defer srv.Close()

	ms, err := NewGithubClient("token", srv.URL).ListOpenMilestones(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Title != "v2 search" {
		t.Fatalf("got %+v", ms)
	}
	// A null due_on must decode to the zero time, not error.
	if !ms[1].DueOn.IsZero() {
		t.Errorf("due_on should be zero, got %v", ms[1].DueOn)
	}
}

func TestGithubListOpenPullRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":90,"title":"feat: authors","body":"Closes #41","draft":true}
		]`))
	}))
	defer srv.Close()

	prs, err := NewGithubClient("token", srv.URL).ListOpenPullRequests(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 90 || !prs[0].Draft {
		t.Fatalf("got %+v", prs)
	}
	if prs[0].Body != "Closes #41" {
		t.Errorf("body: %q", prs[0].Body)
	}
}
```

- [ ] **Step 7: Run and verify both fail**

Run: `go test ./internal/forge/ -run 'TestGithubListOpenMilestones|TestGithubListOpenPullRequests' -v`
Expected: FAIL — `c.ListOpenMilestones undefined`

- [ ] **Step 8: Implement the three new client methods**

In `internal/forge/client.go`, add:

```go
// Milestone is an open milestone. DueOn is the zero time when unset.
type Milestone struct {
	Number int       `json:"number"`
	Title  string    `json:"title"`
	DueOn  time.Time `json:"due_on,omitempty"`
}

// PullRequest is an open pull request. Body is needed to resolve "Closes #N".
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
}
```

In `internal/forge/github.go`, add:

```go
func (c *GithubClient) ListOpenMilestones(ctx context.Context, owner, repo string) ([]Milestone, error) {
	var raw []struct {
		Number int        `json:"number"`
		Title  string     `json:"title"`
		DueOn  *time.Time `json:"due_on"`
	}
	if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/milestones?state=open&per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]Milestone, 0, len(raw))
	for _, m := range raw {
		ms := Milestone{Number: m.Number, Title: m.Title}
		if m.DueOn != nil {
			ms.DueOn = *m.DueOn
		}
		out = append(out, ms)
	}
	return out, nil
}

func (c *GithubClient) ListOpenPullRequests(ctx context.Context, owner, repo string) ([]PullRequest, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Draft  bool   `json:"draft"`
	}
	if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/pulls?state=open&per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(raw))
	for _, p := range raw {
		out = append(out, PullRequest{Number: p.Number, Title: p.Title, Body: p.Body, Draft: p.Draft})
	}
	return out, nil
}

// RemoveLabel deletes one label from an issue. A 404 (label not present) is
// not an error — removal is idempotent by design, because the dispatcher
// re-runs label cleanup on every retry tick.
func (c *GithubClient) RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d/labels/%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), number, url.PathEscape(label))
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("remove label: %s", resp.Status)
	}
	return nil
}
```

Note: match the surrounding file's field names for the client's HTTP client and token — read `internal/forge/github.go:16-33` and use whatever `NewGithubClient` actually assigns rather than assuming `c.hc` / `c.token`.

- [ ] **Step 9: Run the full forge suite**

Run: `go test ./internal/forge/ -v`
Expected: PASS, including the pre-existing tests — the `Issue` struct change must not break `TestGithubListRepos` or the cache tests.

- [ ] **Step 10: Commit**

```bash
git add internal/forge/
git commit -m "feat(forge): read milestones, created-at and open PRs"
```

---

### Task 2: Config loading

**Files:**
- Create: `internal/dispatch/types.go`
- Create: `internal/dispatch/config.go`
- Test: `internal/dispatch/config_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Limits struct { GlobalOpenPRs int; PerRepo int; MaxDispatchesPerNight int; Overrides map[string]int }`
  - `type Schedule struct { DispatchAt string; RetryUntil string }`
  - `type Config struct { Limits Limits; Schedule Schedule }`
  - `func DefaultConfig() Config`
  - `func LoadConfig(path string) (Config, error)` — a missing file returns `DefaultConfig(), nil`
  - `func (c Config) LimitFor(repo string) int` — `repo` is the bare repo name (`"quotes"`), not `owner/name`

- [ ] **Step 1: Write the failing test**

```go
package dispatch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if c.Limits.GlobalOpenPRs != 3 || c.Limits.PerRepo != 1 || c.Limits.MaxDispatchesPerNight != 5 {
		t.Errorf("defaults: %+v", c.Limits)
	}
	if c.Schedule.DispatchAt != "22:00" || c.Schedule.RetryUntil != "06:00" {
		t.Errorf("schedule: %+v", c.Schedule)
	}
}

func TestLoadConfigPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	os.WriteFile(path, []byte(`{"limits":{"global_open_prs":7}}`), 0o600)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.GlobalOpenPRs != 7 {
		t.Errorf("override not applied: %d", c.Limits.GlobalOpenPRs)
	}
	// Unset keys must not become zero — a zero per_repo would dispatch nothing.
	if c.Limits.PerRepo != 1 {
		t.Errorf("per_repo should stay default, got %d", c.Limits.PerRepo)
	}
}

func TestLimitForUsesOverride(t *testing.T) {
	c := DefaultConfig()
	c.Limits.Overrides = map[string]int{"quotes": 2}
	if got := c.LimitFor("quotes"); got != 2 {
		t.Errorf("override: %d", got)
	}
	if got := c.LimitFor("bridge"); got != 1 {
		t.Errorf("default: %d", got)
	}
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./internal/dispatch/ -v`
Expected: FAIL — package does not exist / `LoadConfig` undefined

- [ ] **Step 3: Implement**

`internal/dispatch/types.go`:

```go
// Package dispatch decides which enriched issues to hand to the agent-workflow
// pipeline. Everything here except Run is a pure function over plain structs:
// no network, no clock, no filesystem. That is what makes it table-testable.
package dispatch

import "time"

type Limits struct {
	GlobalOpenPRs         int            `json:"global_open_prs"`
	PerRepo               int            `json:"per_repo"`
	MaxDispatchesPerNight int            `json:"max_dispatches_per_night"`
	Overrides             map[string]int `json:"overrides,omitempty"`
}

type Schedule struct {
	DispatchAt string `json:"dispatch_at"`
	RetryUntil string `json:"retry_until"`
}

type Config struct {
	Limits   Limits   `json:"limits"`
	Schedule Schedule `json:"schedule"`
}

// State is the only local mutable state the dispatcher keeps. Everything else
// lives in the forge as labels so it survives a cache wipe.
type State struct {
	Paused            bool      `json:"paused"`
	LastTick          time.Time `json:"last_tick,omitempty"`
	DispatchedTonight int       `json:"dispatched_tonight"`
	NightStartedAt    time.Time `json:"night_started_at,omitempty"`
}
```

`internal/dispatch/config.go`:

```go
package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

func DefaultConfig() Config {
	return Config{
		Limits: Limits{
			GlobalOpenPRs:         3,
			PerRepo:               1,
			MaxDispatchesPerNight: 5,
		},
		Schedule: Schedule{DispatchAt: "22:00", RetryUntil: "06:00"},
	}
}

// LoadConfig reads path over the defaults. A missing file is not an error —
// the zero-config case must work. Unmarshalling into an already-populated
// struct is what keeps unset keys at their defaults rather than zero.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return DefaultConfig(), err
	}
	return c, nil
}

// LimitFor returns the per-repo concurrency limit for a bare repo name.
func (c Config) LimitFor(repo string) int {
	if n, ok := c.Limits.Overrides[repo]; ok {
		return n
	}
	return c.Limits.PerRepo
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/
git commit -m "feat(dispatch): config with defaults and per-repo overrides"
```

---

### Task 3: Eligibility

**Files:**
- Create: `internal/dispatch/eligible.go`
- Test: `internal/dispatch/eligible_test.go`

**Interfaces:**
- Consumes: `forge.Issue` (with `Milestone`, `Created`), `forge.Milestone`, `forge.PullRequest` from Task 1
- Produces:
  - `func Attempts(labels []string) int`
  - `func ActiveMilestone(ms []forge.Milestone) string` — title of the open milestone with the earliest non-zero due date; `""` when none has a due date
  - `func ClosesIssue(prBody string, issueNumber int) bool`
  - `func HasOpenPR(prs []forge.PullRequest, issueNumber int) bool`
  - `func Eligible(i forge.Issue, activeMilestone string, prs []forge.PullRequest) (bool, string)` — returns eligibility plus a human-readable skip reason (`""` when eligible)

- [ ] **Step 1: Write the failing tests**

```go
package dispatch

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestAttempts(t *testing.T) {
	cases := []struct {
		labels []string
		want   int
	}{
		{nil, 0},
		{[]string{"feat"}, 0},
		{[]string{"attempt:1"}, 1},
		{[]string{"feat", "attempt:2"}, 2},
		{[]string{"attempt:notanumber"}, 0},
	}
	for _, c := range cases {
		if got := Attempts(c.labels); got != c.want {
			t.Errorf("Attempts(%v) = %d, want %d", c.labels, got, c.want)
		}
	}
}

func TestActiveMilestoneEarliestDueDate(t *testing.T) {
	d := func(s string) time.Time {
		v, _ := time.Parse("2006-01-02", s)
		return v
	}
	ms := []forge.Milestone{
		{Title: "later", DueOn: d("2026-09-01")},
		{Title: "sooner", DueOn: d("2026-08-15")},
		{Title: "someday"}, // no due date — never active
	}
	if got := ActiveMilestone(ms); got != "sooner" {
		t.Errorf("got %q", got)
	}
	if got := ActiveMilestone([]forge.Milestone{{Title: "someday"}}); got != "" {
		t.Errorf("undated milestone must not be active, got %q", got)
	}
	if got := ActiveMilestone(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestClosesIssue(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{"Closes #41", true},
		{"closes #41", true},
		{"Fixes #41", true},
		{"Resolves #41", true},
		{"body\n\nCloses #41\n", true},
		{"Closes #410", false}, // must not match a longer number
		{"Closes #4", false},
		{"mentions #41 casually", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ClosesIssue(c.body, 41); got != c.want {
			t.Errorf("ClosesIssue(%q, 41) = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestEligible(t *testing.T) {
	base := forge.Issue{Number: 41, Labels: []string{"feat"}, Milestone: "v2 search"}

	cases := []struct {
		name       string
		issue      forge.Issue
		milestone  string
		prs        []forge.PullRequest
		wantOK     bool
		wantReason string
	}{
		{"happy path", base, "v2 search", nil, true, ""},
		{"no active milestone dispatches anyway",
			forge.Issue{Number: 41, Milestone: ""}, "", nil, true, ""},
		{"not enriched",
			forge.Issue{Number: 41, Labels: []string{"needs-enrichment"}}, "", nil,
			false, "needs-enrichment"},
		{"parked",
			forge.Issue{Number: 41, Labels: []string{"🧊 parked"}}, "", nil,
			false, "parked"},
		{"attempt budget spent",
			forge.Issue{Number: 41, Labels: []string{"attempt:2"}}, "", nil,
			false, "attempts exhausted"},
		{"has open PR", base, "v2 search",
			[]forge.PullRequest{{Number: 90, Body: "Closes #41"}},
			false, "open PR"},
		{"outside active milestone",
			forge.Issue{Number: 41, Milestone: "backlog"}, "v2 search", nil,
			false, "outside active milestone"},
	}
	for _, c := range cases {
		ok, reason := Eligible(c.issue, c.milestone, c.prs)
		if ok != c.wantOK || reason != c.wantReason {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, ok, reason, c.wantOK, c.wantReason)
		}
	}
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./internal/dispatch/ -run 'TestAttempts|TestActiveMilestone|TestClosesIssue|TestEligible' -v`
Expected: FAIL — `Attempts undefined`

- [ ] **Step 3: Implement**

`internal/dispatch/eligible.go`:

```go
package dispatch

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

const (
	LabelNeedsEnrichment = "needs-enrichment"
	LabelParked          = "🧊 parked"
	LabelAIImplement     = "ai-implement"
	attemptPrefix        = "attempt:"
	failedPrefix         = "failed:"
)

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// Attempts reads the attempt:N label. Absent or malformed means zero attempts,
// which is the safe direction: a corrupt label lets the issue run, it does not
// silently strand it.
func Attempts(labels []string) int {
	for _, l := range labels {
		if !strings.HasPrefix(l, attemptPrefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(l, attemptPrefix))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// ActiveMilestone is the open milestone with the earliest due date. A milestone
// without a due date is never active — setting a due date is how the operator
// marks a milestone active, so an undated one is explicitly "not now".
func ActiveMilestone(ms []forge.Milestone) string {
	active := ""
	var activeDue time.Time
	for _, m := range ms {
		if m.DueOn.IsZero() {
			continue
		}
		if active == "" || m.DueOn.Before(activeDue) {
			active, activeDue = m.Title, m.DueOn
		}
	}
	return active
}

var closesRE = regexp.MustCompile(`(?i)\b(clos(?:e|es|ed)|fix(?:e|es|ed)?|resolv(?:e|es|ed))\s+#(\d+)\b`)

// ClosesIssue reports whether prBody contains a GitHub closing keyword for
// issueNumber. The \b on the number is what stops "#410" matching issue 41.
func ClosesIssue(prBody string, issueNumber int) bool {
	for _, m := range closesRE.FindAllStringSubmatch(prBody, -1) {
		if n, err := strconv.Atoi(m[2]); err == nil && n == issueNumber {
			return true
		}
	}
	return false
}

// HasOpenPR reports whether any open PR closes this issue. Only closing PRs
// count, so a hand-written PR never consumes a dispatch slot.
func HasOpenPR(prs []forge.PullRequest, issueNumber int) bool {
	for _, p := range prs {
		if ClosesIssue(p.Body, issueNumber) {
			return true
		}
	}
	return false
}

// Eligible reports whether an issue may be dispatched, plus the reason it was
// skipped. activeMilestone is "" when the repo has no dated open milestone, in
// which case milestone membership is not checked at all.
func Eligible(i forge.Issue, activeMilestone string, prs []forge.PullRequest) (bool, string) {
	if hasLabel(i.Labels, LabelNeedsEnrichment) {
		return false, "needs-enrichment"
	}
	if hasLabel(i.Labels, LabelParked) {
		return false, "parked"
	}
	if Attempts(i.Labels) >= 2 {
		return false, "attempts exhausted"
	}
	if HasOpenPR(prs, i.Number) {
		return false, "open PR"
	}
	if activeMilestone != "" && i.Milestone != activeMilestone {
		return false, "outside active milestone"
	}
	return true, ""
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/
git commit -m "feat(dispatch): eligibility rules"
```

---

### Task 4: Priority ordering

**Files:**
- Create: `internal/dispatch/order.go`
- Test: `internal/dispatch/order_test.go`

**Interfaces:**
- Consumes: `forge.Issue` from Task 1
- Produces:
  - `type Candidate struct { Issue forge.Issue; Repo string; Owner string; MilestoneDue time.Time }` — `Repo` is the bare name
  - `func Order(cs []Candidate) []Candidate` — returns a new sorted slice, does not mutate the input

- [ ] **Step 1: Write the failing test**

```go
package dispatch

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func day(s string) time.Time {
	v, _ := time.Parse("2006-01-02", s)
	return v
}

func TestOrderLadder(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"chore"}, Created: day("2026-01-01")}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"bug"}, Created: day("2026-05-01")}},
		{Repo: "c", Issue: forge.Issue{Number: 3, Labels: []string{"feat"}, Created: day("2026-02-01")}},
		{Repo: "d", Issue: forge.Issue{Number: 4, Labels: []string{"bug"}, Created: day("2026-06-01")},
			MilestoneDue: day("2026-08-01")},
	}
	got := Order(cs)
	want := []int{4, 2, 3, 1} // milestone first, then bug, feat, chore
	for i, n := range want {
		if got[i].Issue.Number != n {
			t.Fatalf("position %d: got #%d, want #%d (full order %v)", i, got[i].Issue.Number, n, numbers(got))
		}
	}
}

func TestOrderSizeThenAge(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"feat", "size:l"}, Created: day("2026-01-01")}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"feat", "size:s"}, Created: day("2026-06-01")}},
		{Repo: "c", Issue: forge.Issue{Number: 3, Labels: []string{"feat"}, Created: day("2026-03-01")}},
		{Repo: "d", Issue: forge.Issue{Number: 4, Labels: []string{"feat"}, Created: day("2026-02-01")}},
	}
	got := Order(cs)
	// size:s, then the two unlabelled (= m) oldest-first, then size:l
	want := []int{2, 4, 3, 1}
	for i, n := range want {
		if got[i].Issue.Number != n {
			t.Fatalf("position %d: got #%d, want #%d (full order %v)", i, got[i].Issue.Number, n, numbers(got))
		}
	}
}

func TestOrderDoesNotMutateInput(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"chore"}}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"bug"}}},
	}
	Order(cs)
	if cs[0].Issue.Number != 1 {
		t.Errorf("input was mutated: %v", numbers(cs))
	}
}

func numbers(cs []Candidate) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.Issue.Number
	}
	return out
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./internal/dispatch/ -run TestOrder -v`
Expected: FAIL — `Candidate undefined` / `Order undefined`

- [ ] **Step 3: Implement**

`internal/dispatch/order.go`:

```go
package dispatch

import (
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// Candidate is one issue considered for dispatch, with the repo context the
// ordering ladder needs.
type Candidate struct {
	Issue        forge.Issue
	Owner        string
	Repo         string // bare name, e.g. "quotes"
	MilestoneDue time.Time
}

// typeRank maps an issue's labels to the ladder's second rung.
// Lower sorts first.
func typeRank(labels []string) int {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "bug", "fix":
			return 0
		}
	}
	for _, l := range labels {
		if strings.ToLower(l) == "feat" {
			return 1
		}
	}
	return 2
}

// sizeRank maps size:s|m|l to the ladder's third rung. Unlabelled is "m",
// so an unsized issue never jumps ahead of a deliberate quick win.
func sizeRank(labels []string) int {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "size:s":
			return 0
		case "size:m":
			return 1
		case "size:l":
			return 2
		}
	}
	return 1
}

// Order sorts candidates by the deterministic ladder: milestone due date,
// then type, then size, then age. It returns a new slice.
func Order(cs []Candidate) []Candidate {
	out := slices.Clone(cs)
	slices.SortStableFunc(out, func(a, b Candidate) int {
		if c := compareDue(a.MilestoneDue, b.MilestoneDue); c != 0 {
			return c
		}
		if c := typeRank(a.Issue.Labels) - typeRank(b.Issue.Labels); c != 0 {
			return c
		}
		if c := sizeRank(a.Issue.Labels) - sizeRank(b.Issue.Labels); c != 0 {
			return c
		}
		return a.Issue.Created.Compare(b.Issue.Created)
	})
	return out
}

// compareDue sorts dated milestones before undated ones — a zero time means
// "no milestone", which must sort last rather than earliest.
func compareDue(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	default:
		return a.Compare(b)
	}
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/
git commit -m "feat(dispatch): deterministic priority ladder"
```

---

### Task 5: Caps

**Files:**
- Create: `internal/dispatch/caps.go`
- Test: `internal/dispatch/caps_test.go`

**Interfaces:**
- Consumes: `Candidate` (Task 4), `Config` (Task 2)
- Produces:
  - `type Decision struct { Candidate Candidate; Dispatch bool; Reason string }`
  - `func ApplyCaps(ordered []Candidate, cfg Config, openPRsByRepo map[string]int, globalOpen int, dispatchedTonight int) []Decision`

- [ ] **Step 1: Write the failing test**

```go
package dispatch

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func cand(repo string, n int) Candidate {
	return Candidate{Repo: repo, Issue: forge.Issue{Number: n}}
}

func TestApplyCapsPerRepo(t *testing.T) {
	cfg := DefaultConfig() // per_repo 1, global 3, night 5
	ds := ApplyCaps([]Candidate{cand("quotes", 1), cand("quotes", 2)}, cfg, map[string]int{}, 0, 0)

	if !ds[0].Dispatch {
		t.Errorf("first should dispatch: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "repo at WIP 1/1" {
		t.Errorf("second: %+v", ds[1])
	}
}

func TestApplyCapsCountsExistingOpenPRs(t *testing.T) {
	cfg := DefaultConfig()
	ds := ApplyCaps([]Candidate{cand("quotes", 1)}, cfg, map[string]int{"quotes": 1}, 1, 0)
	if ds[0].Dispatch {
		t.Errorf("repo already at limit, must skip: %+v", ds[0])
	}
}

func TestApplyCapsGlobal(t *testing.T) {
	cfg := DefaultConfig()
	cs := []Candidate{cand("a", 1), cand("b", 2), cand("c", 3), cand("d", 4)}
	ds := ApplyCaps(cs, cfg, map[string]int{}, 0, 0)

	for i := 0; i < 3; i++ {
		if !ds[i].Dispatch {
			t.Errorf("index %d should dispatch: %+v", i, ds[i])
		}
	}
	if ds[3].Dispatch || ds[3].Reason != "global cap 3/3" {
		t.Errorf("fourth: %+v", ds[3])
	}
}

func TestApplyCapsNightlyCeiling(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxDispatchesPerNight = 1
	ds := ApplyCaps([]Candidate{cand("a", 1), cand("b", 2)}, cfg, map[string]int{}, 0, 0)
	if !ds[0].Dispatch {
		t.Errorf("first: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "night cap 1/1" {
		t.Errorf("second: %+v", ds[1])
	}
}

func TestApplyCapsRespectsAlreadyDispatchedTonight(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxDispatchesPerNight = 2
	ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, map[string]int{}, 0, 2)
	if ds[0].Dispatch {
		t.Errorf("night budget spent, must skip: %+v", ds[0])
	}
}

func TestApplyCapsUsesPerRepoOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.Overrides = map[string]int{"quotes": 2}
	ds := ApplyCaps([]Candidate{cand("quotes", 1), cand("quotes", 2)}, cfg, map[string]int{}, 0, 0)
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Errorf("override 2 should allow both: %+v %+v", ds[0], ds[1])
	}
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./internal/dispatch/ -run TestApplyCaps -v`
Expected: FAIL — `ApplyCaps undefined`

- [ ] **Step 3: Implement**

`internal/dispatch/caps.go`:

```go
package dispatch

import "fmt"

// Decision is one candidate's outcome, carrying the reason so --dry-run can
// explain every skip.
type Decision struct {
	Candidate Candidate
	Dispatch  bool
	Reason    string
}

// ApplyCaps walks an ordered candidate list and marks each one dispatch or
// skip, tightening three independent bounds as it goes:
//
//	per-repo    — avoids conflicting concurrent PRs in one repo
//	global      — the operator's review capacity
//	nightly     — bounds unattended spend, which the WIP cap alone cannot
//
// openPRsByRepo and globalOpen are the counts that already exist before this
// tick; dispatchedTonight is how many this night has already produced.
func ApplyCaps(ordered []Candidate, cfg Config, openPRsByRepo map[string]int, globalOpen, dispatchedTonight int) []Decision {
	perRepo := make(map[string]int, len(openPRsByRepo))
	for k, v := range openPRsByRepo {
		perRepo[k] = v
	}
	global := globalOpen
	night := dispatchedTonight

	out := make([]Decision, 0, len(ordered))
	for _, c := range ordered {
		limit := cfg.LimitFor(c.Repo)
		switch {
		case night >= cfg.Limits.MaxDispatchesPerNight:
			out = append(out, Decision{c, false,
				fmt.Sprintf("night cap %d/%d", night, cfg.Limits.MaxDispatchesPerNight)})
		case global >= cfg.Limits.GlobalOpenPRs:
			out = append(out, Decision{c, false,
				fmt.Sprintf("global cap %d/%d", global, cfg.Limits.GlobalOpenPRs)})
		case perRepo[c.Repo] >= limit:
			out = append(out, Decision{c, false,
				fmt.Sprintf("repo at WIP %d/%d", perRepo[c.Repo], limit)})
		default:
			perRepo[c.Repo]++
			global++
			night++
			out = append(out, Decision{c, true, ""})
		}
	}
	return out
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/
git commit -m "feat(dispatch): per-repo, global and nightly caps"
```

---

### Task 6: Failure handling

**Files:**
- Create: `internal/dispatch/failure.go`
- Test: `internal/dispatch/failure_test.go`

**Interfaces:**
- Consumes: `forge.Issue` (Task 1), `Attempts` (Task 3)
- Produces:
  - `func FailureBucket(labels []string) string` — `""` when no `failed:*` label
  - `func IsTransient(bucket string) bool`
  - `type Action struct { AddLabels []string; RemoveLabels []string; Comment string; Retry bool }`
  - `func NextAction(i forge.Issue) Action`

- [ ] **Step 1: Write the failing test**

```go
package dispatch

import (
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestIsTransient(t *testing.T) {
	for _, b := range []string{"api_auth", "rate_limit", "infra"} {
		if !IsTransient(b) {
			t.Errorf("%s should be transient", b)
		}
	}
	for _, b := range []string{"max_turns", "gate_failed", "no_diff", "", "unknown"} {
		if IsTransient(b) {
			t.Errorf("%s should not be transient", b)
		}
	}
}

func TestNextActionTransientRetriesWithoutIncrementing(t *testing.T) {
	i := forge.Issue{Number: 41, Labels: []string{"failed:rate_limit", "ai-implement"}}
	a := NextAction(i)

	if !a.Retry {
		t.Error("transient failure must retry")
	}
	for _, l := range a.AddLabels {
		if strings.HasPrefix(l, "attempt:") {
			t.Errorf("transient must not increment attempts, got %v", a.AddLabels)
		}
	}
	// The stale failure label must be cleared or the next tick re-reads it.
	if len(a.RemoveLabels) != 1 || a.RemoveLabels[0] != "failed:rate_limit" {
		t.Errorf("RemoveLabels: %v", a.RemoveLabels)
	}
}

func TestNextActionSubstantiveIncrementsToOne(t *testing.T) {
	i := forge.Issue{Number: 41, Labels: []string{"failed:max_turns"}}
	a := NextAction(i)

	if !contains(a.AddLabels, "attempt:1") {
		t.Errorf("AddLabels: %v", a.AddLabels)
	}
	if contains(a.AddLabels, LabelParked) {
		t.Error("must not park on the first substantive failure")
	}
	if !a.Retry {
		t.Error("first substantive failure still retries")
	}
}

func TestNextActionSecondSubstantiveParks(t *testing.T) {
	i := forge.Issue{Number: 41, Labels: []string{"failed:gate_failed", "attempt:1"}}
	a := NextAction(i)

	if !contains(a.AddLabels, "attempt:2") {
		t.Errorf("AddLabels: %v", a.AddLabels)
	}
	if !contains(a.AddLabels, LabelParked) {
		t.Errorf("second substantive failure must park: %v", a.AddLabels)
	}
	if a.Retry {
		t.Error("parked issues must not retry")
	}
	if !strings.Contains(a.Comment, "gate_failed") {
		t.Errorf("comment must name the bucket: %q", a.Comment)
	}
	// The old counter must go, or the issue carries attempt:1 and attempt:2.
	if !contains(a.RemoveLabels, "attempt:1") {
		t.Errorf("RemoveLabels: %v", a.RemoveLabels)
	}
}

func TestNextActionNoFailureIsNoop(t *testing.T) {
	a := NextAction(forge.Issue{Number: 41, Labels: []string{"feat"}})
	if a.Retry || len(a.AddLabels) != 0 || len(a.RemoveLabels) != 0 || a.Comment != "" {
		t.Errorf("expected zero Action, got %+v", a)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./internal/dispatch/ -run 'TestIsTransient|TestNextAction' -v`
Expected: FAIL — `IsTransient undefined`

- [ ] **Step 3: Implement**

`internal/dispatch/failure.go`:

```go
package dispatch

import (
	"fmt"
	"strings"

	"github.com/freaxnx01/bridge/internal/forge"
)

const maxAttempts = 2

// transientBuckets are failures that say nothing about the issue itself, so
// retrying costs nothing but a tick. Everything else counts against the
// attempt budget.
var transientBuckets = map[string]bool{
	"api_auth":   true,
	"rate_limit": true,
	"infra":      true,
}

// FailureBucket returns the bucket from a failed:<bucket> label, or "".
func FailureBucket(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, failedPrefix) {
			return strings.TrimPrefix(l, failedPrefix)
		}
	}
	return ""
}

func IsTransient(bucket string) bool { return transientBuckets[bucket] }

// Action is the label and comment work a retry tick must perform on one issue.
type Action struct {
	AddLabels    []string
	RemoveLabels []string
	Comment      string
	Retry        bool
}

// NextAction decides what to do with an issue whose run failed.
//
// A failed run often produces no PR at all, so the WIP slot frees and the
// issue stays eligible. Without the attempt budget the dispatcher would
// re-dispatch it forever, burning money silently. That is the hole this closes.
func NextAction(i forge.Issue) Action {
	bucket := FailureBucket(i.Labels)
	if bucket == "" {
		return Action{}
	}

	a := Action{RemoveLabels: []string{failedPrefix + bucket}}

	if IsTransient(bucket) {
		a.Retry = true
		return a
	}

	attempts := Attempts(i.Labels) + 1
	if attempts > 1 {
		a.RemoveLabels = append(a.RemoveLabels, fmt.Sprintf("%s%d", attemptPrefix, attempts-1))
	}
	a.AddLabels = append(a.AddLabels, fmt.Sprintf("%s%d", attemptPrefix, attempts))

	if attempts >= maxAttempts {
		a.AddLabels = append(a.AddLabels, LabelParked)
		a.Comment = fmt.Sprintf(
			"Parked by `bridge dispatch` after %d failed runs (last failure: `%s`). "+
				"Remove the parked label to let it run again.", attempts, bucket)
		return a
	}

	a.Retry = true
	a.Comment = fmt.Sprintf("Run failed (`%s`). Retrying — attempt %d of %d.", bucket, attempts, maxAttempts)
	return a
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/
git commit -m "feat(dispatch): attempt budget and failure classification"
```

---

### Task 7: Local state (pause, last tick, nightly counter)

**Files:**
- Create: `internal/dispatch/state.go`
- Test: `internal/dispatch/state_test.go`

**Interfaces:**
- Consumes: `State` (Task 2), `store.AtomicWrite`
- Produces:
  - `func ReadState(path string) (State, error)` — missing file returns zero `State`, nil error
  - `func WriteState(path string, s State) error`
  - `func (s State) NightBudgetUsed(now time.Time) int` — resets to 0 once `now` is a different dispatch night than `NightStartedAt`

- [ ] **Step 1: Write the failing test**

```go
package dispatch

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadStateMissingFile(t *testing.T) {
	s, err := ReadState(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if s.Paused || s.DispatchedTonight != 0 {
		t.Errorf("zero state expected: %+v", s)
	}
}

func TestWriteThenReadState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	want := State{Paused: true, DispatchedTonight: 2, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}
	if err := WriteState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Paused || got.DispatchedTonight != 2 || !got.NightStartedAt.Equal(want.NightStartedAt) {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestNightBudgetResetsOnANewNight(t *testing.T) {
	s := State{DispatchedTonight: 4, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}

	// A 02:00 retry tick is the SAME night as the 22:00 dispatch before it.
	sameNight := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	if got := s.NightBudgetUsed(sameNight); got != 4 {
		t.Errorf("same night should keep the count, got %d", got)
	}

	// The next evening is a new night.
	nextNight := time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	if got := s.NightBudgetUsed(nextNight); got != 0 {
		t.Errorf("new night should reset, got %d", got)
	}
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./internal/dispatch/ -run 'State|NightBudget' -v`
Expected: FAIL — `ReadState undefined`

- [ ] **Step 3: Implement**

`internal/dispatch/state.go`:

```go
package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

// ReadState loads the dispatcher's local state. A missing file is the
// first-run case, not an error.
func ReadState(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

func WriteState(path string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

// NightBudgetUsed returns how much of the nightly ceiling this night has
// already spent. A "night" runs from one evening into the following morning,
// so a 02:00 retry tick belongs to the previous calendar day's night — hence
// the 12:00 pivot rather than a date comparison.
func (s State) NightBudgetUsed(now time.Time) int {
	if s.NightStartedAt.IsZero() {
		return 0
	}
	if nightOf(now).Equal(nightOf(s.NightStartedAt)) {
		return s.DispatchedTonight
	}
	return 0
}

// nightOf maps an instant to the date its night began. Anything before noon
// belongs to the previous day's night.
func nightOf(t time.Time) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	if t.Hour() < 12 {
		d = d.AddDate(0, 0, -1)
	}
	return d
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS — all of Tasks 2–7

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/
git commit -m "feat(dispatch): local state with nightly budget reset"
```

---

### Task 8: The `bridge dispatch` command

**Files:**
- Create: `cmd/bridge/dispatch.go`
- Test: `cmd/bridge/dispatch_test.go`
- Read first: `cmd/bridge/issues.go:33-60` (`discoverAllRoots`, `clientFor`, `cacheRoot`), `cmd/bridge/output.go` (`emitJSON`)

**Interfaces:**
- Consumes: everything from Tasks 1–7
- Produces: `bridge dispatch [--dry-run|--json]`, `bridge dispatch now`, `bridge dispatch --auto`, `bridge dispatch pause|resume|status`

- [ ] **Step 1: Write the failing test for rendering**

`cmd/bridge/dispatch_test.go`:

```go
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
```

- [ ] **Step 2: Run and verify it fails**

Run: `go test ./cmd/bridge/ -run TestRenderDecisions -v`
Expected: FAIL — `renderDecisions undefined`

- [ ] **Step 3: Implement rendering only**

Add to `cmd/bridge/dispatch.go`:

```go
package main

import (
	"fmt"
	"io"

	"github.com/freaxnx01/bridge/internal/dispatch"
)

func renderDecisions(w io.Writer, ds []dispatch.Decision) {
	dispatched, skipped := 0, 0
	for _, d := range ds {
		status := "dispatch"
		if !d.Dispatch {
			status = fmt.Sprintf("SKIP (%s)", d.Reason)
			skipped++
		} else {
			dispatched++
		}
		fmt.Fprintf(w, "  %-12s #%-4d %-28s → %s\n",
			d.Candidate.Repo, d.Candidate.Issue.Number, truncate(d.Candidate.Issue.Title, 28), status)
	}
	fmt.Fprintf(w, "\n%d dispatched, %d skipped\n", dispatched, skipped)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
```

- [ ] **Step 4: Run and verify it passes**

Run: `go test ./cmd/bridge/ -run TestRenderDecisions -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/dispatch.go cmd/bridge/dispatch_test.go
git commit -m "feat(dispatch): render dispatch decisions"
```

- [ ] **Step 6: Write the failing test for candidate collection**

Collection is the one piece of Task 8 worth testing directly, because it is
where GitHub-only filtering lives. Add to `cmd/bridge/dispatch_test.go`:

```go
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
```

- [ ] **Step 7: Run and verify it fails**

Run: `go test ./cmd/bridge/ -run TestCollectCandidates -v`
Expected: FAIL — `repoInput undefined`

- [ ] **Step 8: Implement collection**

Add to `cmd/bridge/dispatch.go`:

```go
// repoInput is one repo's fetched state, kept as a plain struct so
// collectCandidates stays testable without a network.
type repoInput struct {
	Forge      string
	Owner      string
	Name       string
	Issues     []forge.Issue
	Milestones []forge.Milestone
	PRs        []forge.PullRequest
}

// collectCandidates filters each repo's issues to the eligible ones.
// Non-GitHub repos are skipped silently: ai-implement runs on GitHub Actions,
// so there is no pipeline to dispatch to elsewhere.
func collectCandidates(repos []repoInput) []dispatch.Candidate {
	var out []dispatch.Candidate
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		active := dispatch.ActiveMilestone(r.Milestones)
		due := milestoneDue(r.Milestones, active)
		for _, i := range r.Issues {
			if ok, _ := dispatch.Eligible(i, active, r.PRs); !ok {
				continue
			}
			out = append(out, dispatch.Candidate{
				Issue: i, Owner: r.Owner, Repo: r.Name, MilestoneDue: due,
			})
		}
	}
	return out
}

func milestoneDue(ms []forge.Milestone, title string) time.Time {
	for _, m := range ms {
		if m.Title == title {
			return m.DueOn
		}
	}
	return time.Time{}
}
```

Add `"time"` and the `forge` import to the file's import block.

- [ ] **Step 9: Run and verify it passes**

Run: `go test ./cmd/bridge/ -run TestCollectCandidates -v`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add cmd/bridge/
git commit -m "feat(dispatch): collect eligible candidates, github only"
```

- [ ] **Step 11: Wire the cobra commands**

Add to `cmd/bridge/dispatch.go`. `fetchRepoInputs` performs the network reads
using `discoverAllRoots()` and `clientFor()` exactly as `cmd/bridge/issues.go`
does — read that file first and mirror its error handling (collect the first
error, skip the failing repo, continue).

```go
var (
	dispatchDryRun bool
	dispatchJSON   bool
	dispatchAuto   bool
)

var dispatchCmd = &cobra.Command{
	Use:   "dispatch",
	Short: "Dispatch eligible issues to the agent-workflow pipeline",
	RunE:  runDispatch,
}

var dispatchNowCmd = &cobra.Command{
	Use:   "now",
	Short: "Run one dispatch tick immediately",
	RunE:  func(cmd *cobra.Command, args []string) error { return runDispatch(cmd, args) },
}

var dispatchPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Stop the dispatcher until resumed",
	RunE:  func(cmd *cobra.Command, args []string) error { return setPaused(cmd, true) },
}

var dispatchResumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume the dispatcher",
	RunE:  func(cmd *cobra.Command, args []string) error { return setPaused(cmd, false) },
}

var dispatchStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show caps, in-flight work, and last tick",
	RunE:  runDispatchStatus,
}

func init() {
	dispatchCmd.Flags().BoolVar(&dispatchDryRun, "dry-run", false, "decide and print, change nothing")
	dispatchCmd.Flags().BoolVar(&dispatchJSON, "json", false, "machine-readable output")
	dispatchCmd.Flags().BoolVar(&dispatchAuto, "auto", false, "timer entry point; honours the pause flag")
	dispatchCmd.AddCommand(dispatchNowCmd, dispatchPauseCmd, dispatchResumeCmd, dispatchStatusCmd)
	rootCmd.AddCommand(dispatchCmd)
}

func dispatchConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "bridge", "dispatch.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bridge", "dispatch.json")
}

func dispatchStatePath() string { return filepath.Join(cacheRoot(), "dispatch.json") }

func runDispatch(cmd *cobra.Command, _ []string) error {
	cfg, err := dispatch.LoadConfig(dispatchConfigPath())
	if err != nil {
		return err
	}
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		return err
	}
	// --auto is the only mode the pause flag gates. An explicit `dispatch now`
	// is the operator asking for it, so it always runs.
	if dispatchAuto && state.Paused {
		fmt.Fprintln(cmd.OutOrStdout(), "dispatcher paused — nothing to do")
		return nil
	}

	repos, err := fetchRepoInputs(context.Background())
	if err != nil {
		return err
	}

	openByRepo, globalOpen := countOpenAgentPRs(repos)
	now := time.Now()
	decisions := dispatch.ApplyCaps(
		dispatch.Order(collectCandidates(repos)),
		cfg, openByRepo, globalOpen, state.NightBudgetUsed(now),
	)

	if dispatchJSON {
		return emitJSON(cmd.OutOrStdout(), decisions)
	}
	renderDecisions(cmd.OutOrStdout(), decisions)
	if dispatchDryRun {
		return nil
	}
	return applyDecisions(context.Background(), decisions, state, now)
}
```

- [ ] **Step 11b: Implement the three I/O helpers**

```go
// fetchRepoInputs reads every discovered repo's issues, milestones and open
// PRs. Mirror the error handling in cmd/bridge/issues.go: keep the first
// error, skip the failing repo, keep going — one unreachable repo must not
// stop the whole tick.
func fetchRepoInputs(ctx context.Context) ([]repoInput, error) {
	repos, err := discoverAllRoots()
	if err != nil {
		return nil, err
	}
	var out []repoInput
	var firstErr error
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		gh, ok := clientFor(r.Forge).(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		in := repoInput{Forge: r.Forge, Owner: r.Owner, Name: r.Name}
		if in.Issues, err = gh.ListOpenIssues(ctx, r.Owner, r.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if in.Milestones, err = gh.ListOpenMilestones(ctx, r.Owner, r.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if in.PRs, err = gh.ListOpenPullRequests(ctx, r.Owner, r.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, in)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// countOpenAgentPRs counts open PRs that close one of the repo's own issues.
// Only those are pipeline output, so a hand-written PR never consumes a slot.
func countOpenAgentPRs(repos []repoInput) (map[string]int, int) {
	byRepo := make(map[string]int, len(repos))
	total := 0
	for _, r := range repos {
		for _, pr := range r.PRs {
			for _, i := range r.Issues {
				if dispatch.ClosesIssue(pr.Body, i.Number) {
					byRepo[r.Name]++
					total++
					break
				}
			}
		}
	}
	return byRepo, total
}

// applyDecisions writes the one label the dispatcher owns, then persists the
// nightly counter. It never writes agent:* or model:* — model choice belongs
// to agent-workflow's classify-task.sh.
func applyDecisions(ctx context.Context, ds []dispatch.Decision, state dispatch.State, now time.Time) error {
	dispatched := 0
	for _, d := range ds {
		if !d.Dispatch {
			continue
		}
		gh, ok := clientFor("github").(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		owner, repo, num := d.Candidate.Owner, d.Candidate.Repo, d.Candidate.Issue.Number
		if _, err := gh.AddLabels(ctx, owner, repo, num, []string{dispatch.LabelAIImplement}); err != nil {
			return fmt.Errorf("label %s#%d: %w", repo, num, err)
		}
		if _, err := gh.CommentIssue(ctx, owner, repo, num,
			"Dispatched by `bridge dispatch`."); err != nil {
			return fmt.Errorf("comment %s#%d: %w", repo, num, err)
		}
		dispatched++
	}
	state.DispatchedTonight = state.NightBudgetUsed(now) + dispatched
	if state.NightBudgetUsed(now) == 0 {
		state.NightStartedAt = now
	}
	state.LastTick = now
	return dispatch.WriteState(dispatchStatePath(), state)
}
```

Note: `clientFor` returns the `forge.Client` interface, which does not carry the
new methods. Read `cmd/bridge/issues.go` and confirm the concrete type assertion
above matches how `clientFor` is actually implemented; if it returns something
other than `*forge.GithubClient`, adapt rather than change `forge.Client`.

- [ ] **Step 12: Verify the command tree builds and runs**

```bash
go build ./... && go vet ./...
go run ./cmd/bridge dispatch --dry-run
go run ./cmd/bridge dispatch status
```

Expected: `--dry-run` prints a decision table and exits 0 without writing any label.

- [ ] **Step 13: Run the whole suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 14: Commit**

```bash
git add cmd/bridge/
git commit -m "feat(dispatch): wire cobra subcommands and label application"
```

---

### Task 9: Docs and the systemd timer

**Files:**
- Modify: `README.md` (the CLI surface block)
- Create: `docs/dispatch.md`
- Create: `docs/systemd/bridge-dispatch.service`, `docs/systemd/bridge-dispatch.timer`

**Interfaces:**
- Consumes: the CLI from Task 8
- Produces: no code

- [ ] **Step 1: Add the commands to the README CLI surface table**

In `README.md`, inside the `## CLI surface` code block, after the `bridge sync` line:

```
bridge dispatch [--dry-run]     # decide which enriched issues to hand to the pipeline
bridge dispatch now             # one tick, manual
bridge dispatch pause|resume    # kill switch
bridge dispatch status          # caps, in-flight, last tick
```

- [ ] **Step 2: Write `docs/dispatch.md`**

Exactly these headings, each written out in full — no summaries pointing elsewhere:

```markdown
# bridge dispatch
## What it does            ← the boundary: bridge schedules, agent-workflow picks the model
## Eligibility             ← the Eligible() rule, verbatim from Task 3
## Priority                ← the four-rung ladder from Task 4
## Caps                    ← per-repo, global, nightly — and why all three exist
## Labels                  ← table: needs-enrichment, 🧊 parked, ai-implement, attempt:N, failed:<bucket>, size:*
## When a run fails        ← transient vs substantive buckets, the 2-attempt budget
## Config                  ← full ~/.config/bridge/dispatch.json example with every key
## Running it              ← --dry-run, now, pause/resume, status
## First week              ← --dry-run only; do not enable the timer until decisions look right
```

Link the spec at the top: `docs/specs/2026-07-27-bridge-dispatcher-design.md`.

- [ ] **Step 3: Write the systemd units**

`docs/systemd/bridge-dispatch.service`:

```ini
[Unit]
Description=bridge dispatch tick
After=network-online.target

[Service]
Type=oneshot
ExecStart=%h/.local/bin/bridge dispatch --auto
```

`docs/systemd/bridge-dispatch.timer`:

```ini
[Unit]
Description=bridge dispatch — 22:00 dispatch, hourly retries until 06:00

[Timer]
OnCalendar=*-*-* 22:00:00
OnCalendar=*-*-* 23,00,01,02,03,04,05,06:00:00
Persistent=false

[Install]
WantedBy=timers.target
```

Document in `docs/dispatch.md` that the timer is **not** enabled during the
first week — run `--dry-run` by hand until the decisions look right.

- [ ] **Step 4: Verify docs build clean**

Run: `just lint` (or the repo's markdown lint recipe if `just lint` does not cover markdown)
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add README.md docs/dispatch.md docs/systemd/
git commit -m "docs(dispatch): usage, config and systemd units"
```

---

## Deferred to a follow-up

The retry tick is specified but **not implemented by this plan**. `NextAction`
(Task 6) is fully built and tested; wiring it to a `--retry-only` mode that
advances in-flight issues without dispatching new ones is a separate, smaller
plan. Land the dispatch path first, run it in `--dry-run` for a week, then add
retries.

Also deferred, per the spec: Forgejo dispatch, the notification channel,
milestone editing in bridge, escalation retries.
