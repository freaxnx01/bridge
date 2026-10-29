# bridge Telegram nav bot (`bridge bot`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `bridge bot`, a Telegram bot whose inline-keyboard buttons offer `bridge nav`'s repo picker and per-repo dashboard (start/open with Remote Control link, stop, new worktree, issue launch, clone, new repo), replacing the Python `bridge-bot/`.

**Architecture:** nav's data loaders and mutations move into a TUI-free `internal/navdata` (nav's `tea.Cmd`s become thin wrappers), the launch resolution and detached tmux start move from `cmd/bridge` into `internal/launch` (`cmd/bridge` keeps aliases/wrappers so its behaviour and tests stay unchanged). A stdlib `internal/telegram` client feeds a pure `internal/bot` state machine (`Update`/`Render`); `cmd/bridge/bot.go` is the runner that executes the bot's actions against navdata/launch with timeouts.

**Tech Stack:** Go 1.26 (module `github.com/freaxnx01/bridge`), stdlib `net/http` + `encoding/json` + `log/slog`, cobra (existing), tmux, systemd user unit, `just`.

**Spec:** `docs/superpowers/specs/2026-10-11-bridge-telegram-nav-bot-design.md`

## Global Constraints

- Tests: stdlib `testing` only, hand-rolled fakes (no testify, no mocks library).
- No new Go modules: `go.mod`/`go.sum` must not change.
- No package-level mutable state (compiled `*regexp.Regexp` values and cobra flag vars, as already used in `cmd/bridge`, are the only package-level vars added).
- `context.Context` is the first parameter of every new function that does I/O.
- Errors wrapped with `%w`; no `os.Exit` below `cmd/`.
- Logging in the bot runner via `log/slog`.
- Every task ends green on: `gofmt -l .` (no output), `go vet ./...`, `go test -race ./...` (or the task's packages), and `GOOS=windows go build ./...`.
- CI also runs `golangci-lint` v2.1.6 with `.golangci.yml` (standard + bodyclose, errorlint, unconvert): no unused functions may be left behind by an extraction.
- Commits: Conventional Commits (`feat(scope): …`, `refactor(scope): …`, `test(scope): …`, `docs: …`, `chore: …`).
- `bridge bot` is unix-only like `bridge launch`; Windows gets a hidden stub command.
- Bot texts are English only.
- Callback data ≤ 64 bytes; buttons carry short ids, never paths or names from Telegram.
- Free-text names must match `^[A-Za-z0-9._-]{1,40}$`.
- Token only in `~/.config/bridge/bot.env` (mode 0600, `TELEGRAM_BOT_TOKEN=…`, `BRIDGE_BOT_ALLOW=<id>[,<id>]`); never logged, never in argv; a missing or too-open file → refuse to start with a clear message.
- Action timeouts: 30 s; 2 min for clone / fetch / create repo (and a forge refresh).
- Telegram 429/5xx/network: retry with backoff, cap 60 s; 409: log "another process polls this token" and back off; "message is not modified": ignored.
- `internal/nav` behaviour is unchanged and its existing tests stay green **without edits**; the same holds for `cmd/bridge`'s existing tests.
- `cmd/bridge` tests take ~10–150 s (they build the binary once); `just test-fast` skips them during the edit loop, run them before each commit that touches `cmd/bridge`.

## Review Focus

1. **An issue title with emoji or a slash** (`🚀 fix/launch: crash`, or 60 emoji): the worktree/branch name must be ASCII (`7-fix-launch-crash`), the session label and every Telegram text/button must stay valid UTF-8 (nav's old byte-slicing `trunc` would cut a rune and Telegram rejects the message). Pinned in Task 2 (`TestIssueWorktreeName`, `TestIssueWorktreeName_LongEmojiTitleStaysValidUTF8`), Task 5 (`TestIssueLaunch_EmojiAndSlashTitle`), Task 6 (`issues_emoji` golden, `TestRender_CallbackDataFitsAndTextIsValidUTF8`).
2. **A flood of taps while an action runs** (impatient double/triple taps on ▶, or tapping ⏹/➕ during a clone): exactly one action runs; every other tap only gets a "⏳ still working" toast and changes nothing. Pinned in Task 5 (`TestFloodOfTapsWhileBusy`); the runner always feeds a result back (even on timeout) so the busy state always clears — Task 7 (`within`).
3. **A token file with mode 0644** (or 0640/0604): `bridge bot` refuses to start, says `chmod 600`, and never echoes the token. Pinned in Task 7 (`TestLoadBotConfig_RefusesGroupOrWorldReadable`, `TestBotE2E_RefusesWorldReadableConfig`).
4. **A non-allowlisted user in a group chat** tapping a perfectly valid button on the allowlisted user's message: nothing happens (no state change, no answer, one log line). Pinned in Task 5 (`TestNotAllowlisted_TapInGroupOnValidButtonDropped`) and Task 7 (`TestBotE2E_IgnoresNonAllowlistedUser`).
5. **The same repo name on two forges** (`github/…/bridge` and `forgejo/bridge`): buttons are distinguishable (`bridge · github` / `bridge · forgejo`) and a tap opens exactly the tapped repo — the bot never re-resolves by name. Pinned in Task 5 (`TestSameNameOnTwoForges_OpensTheTappedOne`), Task 6 (`TestPickerButtons_SameNameDisambiguated`), Task 3 (`launch.TargetFor` takes the resolved `core.Repo`).

Also pinned, outside the top five: a repo with zero worktrees (Task 1 `TestDashRows_ZeroWorktrees_OnlyBaseRow`, Task 6 `dash_zero_worktrees` golden), callback data > 64 bytes (Task 5 `TestOutdatedTaps`, Task 6), the token leaking through a `net/http` error string (Task 4 `TestTransportError_RetriedAndNeverLeaksToken`).

## File Structure

| Path | Task | Responsibility |
|---|---|---|
| `internal/navdata/repos.go` | 1 | Picker entries: labels, local/remote/MRU loading, generic sort/dedup/disambiguate/filter, forge options, `PickerList` |
| `internal/navdata/dash.go` | 1 | Dashboard rows (base row + worktrees + live sessions), session rows, git status parse/read, git fetch |
| `internal/navdata/issues.go` | 1 | Cached open-issue loading |
| `internal/navdata/mutate.go` | 2 | `CreateWorktree`, friendly worktree errors, new-repo choices, clone/create func types |
| `internal/navdata/issuename.go` | 2 | Issue worktree name + label, `Slugify`, rune-safe `TruncRunes` |
| `internal/nav/navdata_adapt.go` | 1 | Conversions between nav's private row types and navdata's |
| `internal/launch/launch.go` | 3 | `Deps`, `Target`, typed errors, repo lookup, default agent, labels, `ResolveTarget`/`TargetFor`/`Finalize`/`PreLaunchSync`/`SessionLive` (portable) |
| `internal/launch/detached.go` | 3 | `StartDetached`, `WaitRCURL`, `Stop` (unix: tmux) |
| `internal/telegram/types.go`, `client.go` | 4 | Bot API types and client with retry/backoff and typed errors |
| `internal/bot/state.go`, `events.go`, `callback.go`, `update.go` | 5 | Per-chat state, events/actions, callback ids, `Update` |
| `internal/bot/render.go`, `testdata/*.golden` | 6 | `Render` → text + keyboard |
| `cmd/bridge/bot.go` | 7 | `bridge bot` command, `bot.env` loading, poll/event loop, rendering via Telegram |
| `cmd/bridge/bot_env.go` | 7 | The bot's I/O: navdata/launch wiring, `startForBot`, timeouts |
| `cmd/bridge/bot_windows.go` | 7 | Hidden Windows stub |
| `docs/systemd/bridge-bot.service`, `docs/telegram-bot.md`, `justfile`, `README.md`, `docs/ideas.md` | 8 | Operations and docs; `bridge-bot/` removed |

---

### Task 1: `internal/navdata` — read side, nav wraps it

**Files:**
- Create: `internal/navdata/repos.go`, `internal/navdata/dash.go`, `internal/navdata/issues.go`
- Create: `internal/navdata/repos_test.go`, `internal/navdata/dash_test.go`, `internal/navdata/issues_test.go`
- Create: `internal/nav/navdata_adapt.go`
- Modify: `internal/nav/data.go`, `internal/nav/format.go`, `internal/nav/types.go`, `internal/nav/update.go`
- Test (must stay green unchanged): every `internal/nav/*_test.go`

**Interfaces:**
- Consumes: existing `core.DiscoverRepos`, `core.LoadMRU`, `core.LoadSlots`, `core.SlotID`, `core.Session`, `core.Slot`, `core.Repo`; `forge.RepoRef`, `forge.Issue`, `forge.ReadRepoCache`, `forge.ReadIssueCache`, `forge.WriteIssueCache`, `forge.IssueCache`; `worktree.List`, `worktree.Primary`, `worktree.Entry`, `worktree.ExecRunner`; `gitauth.CredentialArgs`.
- Produces (package `github.com/freaxnx01/bridge/internal/navdata`):
  - `type RepoEntry struct { Label string; Repo core.Repo; Remote *forge.RepoRef }` with `func (e RepoEntry) Entry() RepoEntry`
  - `type Row interface{ Entry() RepoEntry }`
  - `func RepoLabel(r core.Repo) string`, `func RemoteLabel(r forge.RepoRef) string`
  - `func LocalRepos(ctx context.Context, roots []string) []RepoEntry`
  - `func RemoteEntries(refs []forge.RepoRef) []RepoEntry`, `func RemoteRepos(cachePath string) ([]RepoEntry, error)`, `func RecentPaths(path string) []string`
  - `func SortKey(label string) string`, `func SortRows[T Row](rows []T)`, `func Parts(e RepoEntry) (forgeName, owner, vis, name string)`, `func Key(e RepoEntry) string`
  - `func DedupRemote[T Row](local, remote []T) []T`, `func OwnerQualifiedLabel(e RepoEntry) string`, `func DisambiguateOwners[T Row](rows []T, relabel func(T, string) T) []T`
  - `func MatchesAllTerms(labelLower string, terms []string) bool`, `func FilterRows[T Row](rows []T, q string) []T`, `func MatchesForge(e RepoEntry, forgeKey string) bool`
  - `type ForgeOpt struct{ Key, Label string }`, `func ForgeOptions() []ForgeOpt`, `func PresentForges(local, remote []RepoEntry) []ForgeOpt`
  - `func PickerList(local, remote []RepoEntry, forgeKey, query string) []RepoEntry`
  - `type DashRow struct { Worktree, Branch, Path, Agent, SlotID, State, LastAccessed string; HasSession bool; DisplayLabel string; IsBase bool }`
  - `type Dirty struct { Modified, Ahead, Behind int; NoUpstream, Clean bool }`
  - `type SessionRow struct { SlotID, RepoLabel, Worktree, Agent, State, LastAccessed string }`
  - `func LastAccessed(sess core.Session, now time.Time) string`, `func HumanLastAccessed(d time.Duration) string`
  - `func SessionRows(live []core.Session, slots []core.Slot, now time.Time) []SessionRow`, `func SlotRepoMatches(slotRepo string, repo core.Repo) bool`
  - `func BuildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []DashRow`
  - `func DashRows(ctx context.Context, live func() ([]core.Session, error), repo core.Repo, slotsPath string) []DashRow`
  - `func ParseDirtyStatus(out string) Dirty`, `func GitDirty(ctx context.Context, path string) (Dirty, error)`
  - `func BuildFetchCmd(ctx context.Context, path, forgeName string, direnvAvailable bool) *exec.Cmd`, `func HaveDirenv() bool`, `func GitFetch(ctx context.Context, path, forgeName string) error`
  - `const IssueCacheTTL = 10 * time.Minute`, `type IssueSource struct { CacheDir string; Fetch func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error) }`
  - `func IssueCacheFile(dir, forgeName, owner, repo string) string`, `func IssuesCached(ctx context.Context, src IssueSource, forgeName, owner, repo string) []forge.Issue`

- [ ] **Step 1: Write the failing navdata tests**

Create `internal/navdata/repos_test.go`:

```go
package navdata

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func mkRepos(t *testing.T, rel ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range rel {
		if err := os.MkdirAll(filepath.Join(root, p, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func labels(es []RepoEntry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Label)
	}
	return out
}

func TestLocalRepos_SortedDedupedAcrossRoots(t *testing.T) {
	root := mkRepos(t, "github/freaxnx01/public/zeta", "github/freaxnx01/public/alpha", "git-forgejo/notes")
	got := LocalRepos(context.Background(), []string{root, root})
	want := []string{"forgejo/notes", "github/public/alpha", "github/public/zeta"}
	if !reflect.DeepEqual(labels(got), want) {
		t.Errorf("labels = %v, want %v", labels(got), want)
	}
}

func TestLocalRepos_MissingRootIsSkipped(t *testing.T) {
	root := mkRepos(t, "github/freaxnx01/public/alpha")
	got := LocalRepos(context.Background(), []string{filepath.Join(root, "nope"), root})
	if len(got) != 1 || got[0].Repo.Name != "alpha" {
		t.Errorf("got %+v, want only alpha", got)
	}
}

func TestRemoteEntries_PrefixedAndSorted(t *testing.T) {
	got := RemoteEntries([]forge.RepoRef{
		{Forge: "github", Owner: "o", Name: "zz", Visibility: "private"},
		{Forge: "gitlab", Owner: "o", Name: "aa"},
	})
	want := []string{"↓ github/private/zz", "↓ gitlab/o/aa"}
	if !reflect.DeepEqual(labels(got), want) {
		t.Errorf("labels = %v, want %v", labels(got), want)
	}
	if got[0].Remote == nil || got[0].Remote.Name != "zz" {
		t.Errorf("remote ref not carried: %+v", got[0])
	}
}

func TestRemoteRepos_MissingCacheIsEmpty(t *testing.T) {
	got, err := RemoteRepos(filepath.Join(t.TempDir(), "remote.list"))
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want empty, nil", got, err)
	}
}

func TestPickerList_DedupsClonedRemoteAndDisambiguatesOwners(t *testing.T) {
	local := []RepoEntry{
		{Label: "github/public/tool", Repo: core.Repo{Name: "tool", Forge: "github", Owner: "freaxnx01", Visibility: "public"}},
	}
	remote := RemoteEntries([]forge.RepoRef{
		{Forge: "github", Owner: "FREAXNX01", Name: "tool", Visibility: "public"}, // already cloned (case differs)
		{Forge: "github", Owner: "acme", Name: "tool", Visibility: "public"},      // same label, other owner
	})
	got := labels(PickerList(local, remote, "", ""))
	want := []string{"github/public/freaxnx01/tool", "↓ github/public/acme/tool"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

func TestPickerList_ForgeAndQuery(t *testing.T) {
	local := []RepoEntry{
		{Label: "github/public/bridge", Repo: core.Repo{Name: "bridge", Forge: "github"}},
		{Label: "forgejo/bridge", Repo: core.Repo{Name: "bridge", Forge: "forgejo"}},
		{Label: "forgejo/notes", Repo: core.Repo{Name: "notes", Forge: "forgejo"}},
	}
	got := labels(PickerList(local, nil, "forgejo", "BRI"))
	if !reflect.DeepEqual(got, []string{"forgejo/bridge"}) {
		t.Errorf("labels = %v", got)
	}
}

func TestPresentForges_CanonicalOrder(t *testing.T) {
	local := []RepoEntry{{Repo: core.Repo{Forge: "forgejo"}}, {Repo: core.Repo{Forge: "github"}}}
	got := PresentForges(local, nil)
	want := []ForgeOpt{{"github", "GitHub"}, {"forgejo", "Forgejo"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRecentPaths_MissingFileIsEmpty(t *testing.T) {
	if got := RecentPaths(filepath.Join(t.TempDir(), "mru")); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
```

Create `internal/navdata/dash_test.go` (it needs `git` on PATH, like the existing `internal/worktree` tests):

```go
package navdata

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

// gitRepo creates a real git repo with one commit on branch main.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir,
			"-c", "user.name=t", "-c", "user.email=t@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func TestDashRows_ZeroWorktrees_OnlyBaseRow(t *testing.T) {
	dir := gitRepo(t)
	repo := core.Repo{Name: "solo", Path: dir}
	rows := DashRows(context.Background(), nil, repo, filepath.Join(t.TempDir(), "slots.json"))
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly the base row", rows)
	}
	if !rows[0].IsBase || rows[0].Branch != "main" || rows[0].Path != dir || rows[0].HasSession {
		t.Errorf("base row = %+v", rows[0])
	}
}

func TestDashRows_WorktreeWithLiveSlot(t *testing.T) {
	dir := gitRepo(t)
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-q", "-b", "worktree-feat",
		filepath.Join(dir, ".worktrees", "feat")).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	slots := filepath.Join(t.TempDir(), "slots.json")
	if err := core.UpsertSlot(slots, core.Slot{ID: "solo-wt-feat", Repo: "solo", Worktree: "feat", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	live := func() ([]core.Session, error) {
		return []core.Session{{SlotID: "solo-wt-feat", State: "detached", LastActivity: time.Now()}}, nil
	}
	rows := DashRows(context.Background(), live, core.Repo{Name: "solo", Path: dir}, slots)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want base + feat", rows)
	}
	wt := rows[1]
	if wt.Worktree != "feat" || !wt.HasSession || wt.SlotID != "solo-wt-feat" || wt.Agent != "claude" || wt.State != "detached" {
		t.Errorf("worktree row = %+v", wt)
	}
}

func TestDashRows_NotAGitRepo_StillHasBaseRow(t *testing.T) {
	rows := DashRows(context.Background(), nil, core.Repo{Name: "x", Path: t.TempDir()}, "")
	if len(rows) != 1 || !rows[0].IsBase || rows[0].Branch != "" {
		t.Errorf("rows = %+v, want one base row with empty branch", rows)
	}
}

func TestParseDirtyStatus(t *testing.T) {
	cases := []struct {
		out  string
		want Dirty
	}{
		{"## main...origin/main\n", Dirty{Clean: true}},
		{"## main...origin/main [ahead 2, behind 3]\n M a.go\n?? b\n", Dirty{Modified: 2, Ahead: 2, Behind: 3}},
		{"## feat\n", Dirty{NoUpstream: true, Clean: true}},
	}
	for _, c := range cases {
		if got := ParseDirtyStatus(c.out); got != c.want {
			t.Errorf("ParseDirtyStatus(%q) = %+v, want %+v", c.out, got, c.want)
		}
	}
}

func TestGitDirty_RealRepo(t *testing.T) {
	d, err := GitDirty(context.Background(), gitRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Clean || !d.NoUpstream {
		t.Errorf("dirty = %+v, want clean + no upstream", d)
	}
}

func TestSessionRows_FallsBackToSlotID(t *testing.T) {
	now := time.Now()
	rows := SessionRows([]core.Session{{SlotID: "x", State: "attached", LastActivity: now}}, nil, now)
	if len(rows) != 1 || rows[0].RepoLabel != "x" || rows[0].LastAccessed != "0m" {
		t.Errorf("rows = %+v", rows)
	}
}
```

Create `internal/navdata/issues_test.go`:

```go
package navdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestIssuesCached_FreshCacheSkipsFetch(t *testing.T) {
	dir := t.TempDir()
	if err := forge.WriteIssueCache(IssueCacheFile(dir, "github", "o", "r"),
		forge.IssueCache{UpdatedAt: time.Now(), Issues: []forge.Issue{{Number: 7}}}); err != nil {
		t.Fatal(err)
	}
	called := false
	src := IssueSource{CacheDir: dir, Fetch: func(context.Context, string, string, string) ([]forge.Issue, error) {
		called = true
		return nil, nil
	}}
	got := IssuesCached(context.Background(), src, "github", "o", "r")
	if called || len(got) != 1 || got[0].Number != 7 {
		t.Errorf("got %+v (fetch called=%v), want cached #7 without fetch", got, called)
	}
}

func TestIssuesCached_FetchErrorFallsBackToStaleCache(t *testing.T) {
	dir := t.TempDir()
	if err := forge.WriteIssueCache(IssueCacheFile(dir, "github", "o", "r"),
		forge.IssueCache{UpdatedAt: time.Now().Add(-time.Hour), Issues: []forge.Issue{{Number: 3}}}); err != nil {
		t.Fatal(err)
	}
	src := IssueSource{CacheDir: dir, Fetch: func(context.Context, string, string, string) ([]forge.Issue, error) {
		return nil, errors.New("offline")
	}}
	if got := IssuesCached(context.Background(), src, "github", "o", "r"); len(got) != 1 || got[0].Number != 3 {
		t.Errorf("got %+v, want stale #3", got)
	}
}

func TestIssuesCached_FetchRewritesCache(t *testing.T) {
	dir := t.TempDir()
	src := IssueSource{CacheDir: dir, Fetch: func(context.Context, string, string, string) ([]forge.Issue, error) {
		return []forge.Issue{{Number: 9}}, nil
	}}
	_ = IssuesCached(context.Background(), src, "github", "o", "r")
	c, err := forge.ReadIssueCache(IssueCacheFile(dir, "github", "o", "r"))
	if err != nil || len(c.Issues) != 1 || c.Issues[0].Number != 9 {
		t.Errorf("cache = %+v, %v; want #9", c, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/navdata/`
Expected: FAIL — build errors such as `undefined: LocalRepos`, `undefined: DashRows`, `undefined: IssuesCached` (the package has no non-test files yet).

- [ ] **Step 3: Implement `internal/navdata` (moved verbatim from nav, exported, context-taking)**

Create `internal/navdata/repos.go`:

```go
// Package navdata holds nav's data loading and mutations as plain,
// context-taking functions with no Bubble Tea dependency, so the `bridge nav`
// TUI and the `bridge bot` Telegram front end always show the same state.
package navdata

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// RepoEntry is one picker entry: a local repo, or a remote-only repo
// (Remote != nil) offered for clone-on-select.
type RepoEntry struct {
	Label  string         // e.g. "github/public/bridge"; remote entries carry a "↓ " prefix
	Repo   core.Repo      // zero for remote-only entries
	Remote *forge.RepoRef // non-nil => clone-on-select
}

// Entry lets RepoEntry satisfy Row.
func (e RepoEntry) Entry() RepoEntry { return e }

// Row is any picker row that wraps a RepoEntry: nav's repoRow (which adds
// issue counts) or RepoEntry itself. The generic list helpers below accept any
// Row so nav and the bot share one implementation.
type Row interface{ Entry() RepoEntry }

// RepoLabel is the picker label for a local repo.
func RepoLabel(r core.Repo) string {
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

// RemoteLabel is the picker label (without the "↓ " prefix) for a remote ref.
func RemoteLabel(r forge.RepoRef) string {
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

// LocalRepos discovers the repos under roots, deduped by path (first root
// wins) and sorted by label. A root that fails discovery is skipped: nav's
// loaders are best effort.
func LocalRepos(ctx context.Context, roots []string) []RepoEntry {
	var out []RepoEntry
	seen := map[string]bool{}
	for _, root := range roots {
		if ctx.Err() != nil {
			break
		}
		repos, err := core.DiscoverRepos(root)
		if err != nil {
			continue
		}
		for _, r := range repos {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			out = append(out, RepoEntry{Label: RepoLabel(r), Repo: r})
		}
	}
	SortRows(out)
	return out
}

// RemoteEntries builds sorted, "↓ "-prefixed entries from forge refs.
func RemoteEntries(refs []forge.RepoRef) []RepoEntry {
	out := make([]RepoEntry, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		out = append(out, RepoEntry{Label: "↓ " + RemoteLabel(ref), Remote: &ref})
	}
	SortRows(out)
	return out
}

// RemoteRepos reads the remote-repo cache (remote.list). A missing cache is
// not an error (it yields no entries).
func RemoteRepos(cachePath string) ([]RepoEntry, error) {
	c, err := forge.ReadRepoCache(cachePath)
	if err != nil {
		return nil, fmt.Errorf("read remote cache %s: %w", cachePath, err)
	}
	return RemoteEntries(c.Repos), nil
}

// RecentPaths returns the MRU file's repo paths, most-recent-first. A read
// error yields an empty list (best effort, like nav).
func RecentPaths(path string) []string {
	paths, _ := core.LoadMRU(path)
	return paths
}

// SortKey is the case-insensitive sort key of a label, ignoring the remote
// "↓ " prefix so local and remote rows compare on the same key.
func SortKey(label string) string {
	return strings.ToLower(strings.TrimPrefix(label, "↓ "))
}

// SortRows orders rows by SortKey(label). Stable.
func SortRows[T Row](rows []T) {
	sort.SliceStable(rows, func(i, j int) bool {
		return SortKey(rows[i].Entry().Label) < SortKey(rows[j].Entry().Label)
	})
}

// Parts returns forge, owner, visibility and name of an entry, from its
// remote ref when present, otherwise its local repo.
func Parts(e RepoEntry) (forgeName, owner, vis, name string) {
	if e.Remote != nil {
		return e.Remote.Forge, e.Remote.Owner, e.Remote.Visibility, e.Remote.Name
	}
	return e.Repo.Forge, e.Repo.Owner, e.Repo.Visibility, e.Repo.Name
}

// Key is the case-insensitive forge+owner+name identity of an entry. The local
// owner comes from the on-disk path, the remote owner from the forge API, and
// their casing can differ (#124).
func Key(e RepoEntry) string {
	f, o, _, n := Parts(e)
	return strings.ToLower(f + "\x00" + o + "\x00" + n)
}

// DedupRemote drops remote rows whose repo is already cloned locally.
func DedupRemote[T Row](local, remote []T) []T {
	have := make(map[string]bool, len(local))
	for _, r := range local {
		have[Key(r.Entry())] = true
	}
	out := make([]T, 0, len(remote))
	for _, r := range remote {
		if have[Key(r.Entry())] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// OwnerQualifiedLabel returns e's label with the owner injected (github only:
// github/<vis>/<owner>/<name>); other forges already carry the owner. The
// remote "↓ " prefix is preserved.
func OwnerQualifiedLabel(e RepoEntry) string {
	f, owner, vis, name := Parts(e)
	if f != "github" {
		return e.Label
	}
	if vis == "" {
		vis = "-"
	}
	display := "github/" + vis + "/" + owner + "/" + name
	if e.Remote != nil {
		return "↓ " + display
	}
	return display
}

// DisambiguateOwners owner-qualifies the labels of rows that render
// identically but belong to different owners. relabel returns a copy of a row
// with a new label. Returns a new slice; rows is not mutated.
func DisambiguateOwners[T Row](rows []T, relabel func(T, string) T) []T {
	idxByLabel := map[string][]int{}
	ownersByLabel := map[string]map[string]bool{}
	for i, r := range rows {
		key := SortKey(r.Entry().Label)
		idxByLabel[key] = append(idxByLabel[key], i)
		if ownersByLabel[key] == nil {
			ownersByLabel[key] = map[string]bool{}
		}
		_, owner, _, _ := Parts(r.Entry())
		ownersByLabel[key][strings.ToLower(owner)] = true
	}
	out := append([]T{}, rows...)
	for key, idxs := range idxByLabel {
		if len(ownersByLabel[key]) < 2 {
			continue
		}
		for _, i := range idxs {
			out[i] = relabel(out[i], OwnerQualifiedLabel(out[i].Entry()))
		}
	}
	return out
}

// MatchesAllTerms reports whether every term is a substring of labelLower.
// Both must already be lower-cased. No terms is vacuously true.
func MatchesAllTerms(labelLower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(labelLower, t) {
			return false
		}
	}
	return true
}

// FilterRows keeps rows whose label contains every whitespace-separated term
// of q (case-insensitive AND). A blank q returns rows unchanged.
func FilterRows[T Row](rows []T, q string) []T {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	terms := strings.Fields(strings.ToLower(q))
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		if MatchesAllTerms(strings.ToLower(r.Entry().Label), terms) {
			out = append(out, r)
		}
	}
	return out
}

// MatchesForge reports whether e belongs to forgeKey; "" (All) matches all.
func MatchesForge(e RepoEntry, forgeKey string) bool {
	if forgeKey == "" {
		return true
	}
	f, _, _, _ := Parts(e)
	return f == forgeKey
}

// ForgeOpt is one forge-filter option: the forge key and its display label.
type ForgeOpt struct {
	Key, Label string
}

// ForgeOptions is the canonical display order of the forge filter options.
func ForgeOptions() []ForgeOpt {
	return []ForgeOpt{
		{"github", "GitHub"},
		{"gitlab", "GitLab"},
		{"forgejo", "Forgejo"},
		{"ado", "ADO"},
	}
}

// PresentForges returns the options for forges that have at least one entry
// in local or remote, in canonical order.
func PresentForges(local, remote []RepoEntry) []ForgeOpt {
	present := map[string]bool{}
	for _, rows := range [][]RepoEntry{local, remote} {
		for _, e := range rows {
			if f, _, _, _ := Parts(e); f != "" {
				present[f] = true
			}
		}
	}
	var out []ForgeOpt
	for _, o := range ForgeOptions() {
		if present[o.Key] {
			out = append(out, o)
		}
	}
	return out
}

// PickerList is nav's visible picker list: local entries, then remote entries
// not cloned locally, owner-disambiguated, scoped to forgeKey ("" = All) and
// filtered by query.
func PickerList(local, remote []RepoEntry, forgeKey, query string) []RepoEntry {
	all := append([]RepoEntry{}, local...)
	all = append(all, DedupRemote(local, remote)...)
	all = DisambiguateOwners(all, func(e RepoEntry, label string) RepoEntry {
		e.Label = label
		return e
	})
	if forgeKey != "" {
		scoped := make([]RepoEntry, 0, len(all))
		for _, e := range all {
			if MatchesForge(e, forgeKey) {
				scoped = append(scoped, e)
			}
		}
		all = scoped
	}
	return FilterRows(all, query)
}
```

Create `internal/navdata/dash.go`:

```go
package navdata

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/gitauth"
	"github.com/freaxnx01/bridge/internal/worktree"
)

// DashRow is one dashboard row: a worktree (or the pinned base checkout),
// optionally with a live session.
type DashRow struct {
	Worktree     string // basename; "" for the base row
	Branch       string
	Path         string
	Agent        string // "" when no live session
	SlotID       string // "" when no live session
	State        string // tmux: attached|detached · Herdr: working|idle|blocked|done|unknown
	LastAccessed string
	HasSession   bool
	// DisplayLabel overrides the default "<repo> [<wt>]" session name ("#123 [<title>]").
	DisplayLabel string
	// IsBase marks the pinned base-checkout row: it launches in the repo root
	// with the bare "<repo>" slot (Worktree == "").
	IsBase bool
}

// Dirty is a worktree's git status. Behind is only accurate after a fetch.
type Dirty struct {
	Modified   int
	Ahead      int
	Behind     int
	NoUpstream bool
	Clean      bool
}

// SessionRow is one global active-session row.
type SessionRow struct {
	SlotID       string
	RepoLabel    string
	Worktree     string
	Agent        string
	State        string
	LastAccessed string
}

// LastAccessed renders a session's "last accessed" cell: the humanized age,
// or — when the backend reports no timestamp (Herdr) — its lifecycle state.
func LastAccessed(sess core.Session, now time.Time) string {
	if !sess.LastActivity.IsZero() {
		return HumanLastAccessed(now.Sub(sess.LastActivity))
	}
	if sess.State != "" {
		return sess.State
	}
	return "—"
}

// HumanLastAccessed renders d as at most two descending units (d/h/m).
func HumanLastAccessed(d time.Duration) string {
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

// SessionRows pairs each live session with its slot record. Sessions created
// outside bridge have no slot, so RepoLabel falls back to the slot id.
func SessionRows(live []core.Session, slots []core.Slot, now time.Time) []SessionRow {
	bySlot := make(map[string]core.Slot, len(slots))
	for _, s := range slots {
		bySlot[s.ID] = s
	}
	rows := make([]SessionRow, 0, len(live))
	for _, s := range live {
		row := SessionRow{SlotID: s.SlotID, State: s.State, LastAccessed: LastAccessed(s, now)}
		if sl, ok := bySlot[s.SlotID]; ok {
			row.RepoLabel = sl.Repo
			row.Worktree = sl.Worktree
			row.Agent = sl.Agent
		} else {
			row.RepoLabel = s.SlotID
		}
		rows = append(rows, row)
	}
	return rows
}

// SlotRepoMatches reports whether a slot's Repo field ("bridge" or
// "freaxnx01/bridge") refers to repo.
func SlotRepoMatches(slotRepo string, repo core.Repo) bool {
	if strings.EqualFold(slotRepo, repo.Name) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(slotRepo), "/"+strings.ToLower(repo.Name))
}

// BuildDashRows joins the repo's worktrees with slots and live sessions. The
// base row comes first; sessioned worktrees follow by last activity (newest
// first), then session-less worktrees by name.
func BuildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []DashRow {
	liveBySlot := make(map[string]core.Session, len(sessions))
	for _, s := range sessions {
		liveBySlot[s.SlotID] = s
	}
	slotByWt := make(map[string]core.Slot)
	for _, sl := range slots {
		if SlotRepoMatches(sl.Repo, repo) && sl.Worktree != "" {
			slotByWt[sl.Worktree] = sl
		}
	}
	rows := make([]DashRow, 0, len(wts))
	for _, e := range wts {
		name := filepath.Base(e.Path)
		row := DashRow{Worktree: name, Branch: e.Branch, Path: e.Path}
		if sl, ok := slotByWt[name]; ok {
			if sess, live := liveBySlot[sl.ID]; live {
				row.HasSession = true
				row.SlotID = sl.ID
				row.Agent = sl.Agent
				row.State = sess.State
				row.LastAccessed = LastAccessed(sess, now)
			}
		}
		rows = append(rows, row)
	}
	sortDashRows(rows, liveBySlot)
	return append([]DashRow{baseRow(repo, baseBranch, slots, liveBySlot, now)}, rows...)
}

// baseRow builds the pinned base-checkout row (slot "<repo>", worktree "").
func baseRow(repo core.Repo, branch string, slots []core.Slot, liveBySlot map[string]core.Session, now time.Time) DashRow {
	row := DashRow{IsBase: true, Branch: branch, Path: repo.Path}
	id := core.SlotID(repo.Name, "")
	sess, live := liveBySlot[id]
	if !live {
		return row
	}
	row.HasSession = true
	row.SlotID = id
	row.State = sess.State
	row.LastAccessed = LastAccessed(sess, now)
	for _, sl := range slots {
		if sl.ID == id {
			row.Agent = sl.Agent
			break
		}
	}
	return row
}

func sortDashRows(rows []DashRow, liveBySlot map[string]core.Session) {
	activity := func(r DashRow) (time.Time, bool) {
		if !r.HasSession {
			return time.Time{}, false
		}
		return liveBySlot[r.SlotID].LastActivity, true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aok := activity(rows[i])
		bj, bok := activity(rows[j])
		if aok != bok {
			return aok
		}
		if aok && bok {
			return ai.After(bj)
		}
		return rows[i].Worktree < rows[j].Worktree
	})
}

// DashRows loads the dashboard rows for repo: its worktrees, the primary
// checkout's branch, the slot registry at slotsPath and the live sessions
// from live (nil => none). Read failures degrade to fewer rows; the base row
// is always present.
func DashRows(ctx context.Context, live func() ([]core.Session, error), repo core.Repo, slotsPath string) []DashRow {
	if ctx.Err() != nil {
		return nil
	}
	r := worktree.ExecRunner{}
	wts, _ := worktree.List(r, repo.Path)
	primary, _ := worktree.Primary(r, repo.Path)
	slots, _ := core.LoadSlots(slotsPath)
	var sessions []core.Session
	if live != nil {
		sessions, _ = live()
	}
	return BuildDashRows(repo, primary.Branch, wts, slots, sessions, time.Now())
}

// ParseDirtyStatus parses `git status --porcelain=v1 --branch` output.
func ParseDirtyStatus(out string) Dirty {
	info := Dirty{}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			header := l[len("## "):]
			info.NoUpstream = !strings.Contains(header, "...")
			if i := strings.IndexByte(header, '['); i >= 0 {
				if j := strings.IndexByte(header[i:], ']'); j >= 0 {
					bracket := header[i+1 : i+j]
					info.Ahead = trackToken(bracket, "ahead ")
					info.Behind = trackToken(bracket, "behind ")
				}
			}
			continue
		}
		info.Modified++
	}
	info.Clean = info.Modified == 0
	return info
}

func trackToken(bracket, key string) int {
	i := strings.Index(bracket, key)
	if i < 0 {
		return 0
	}
	rest := bracket[i+len(key):]
	end := len(rest)
	for k, c := range rest {
		if c < '0' || c > '9' {
			end = k
			break
		}
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// GitDirty runs `git status --porcelain=v1 --branch` in path.
func GitDirty(ctx context.Context, path string) (Dirty, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain=v1", "--branch").Output()
	if err != nil {
		return Dirty{}, fmt.Errorf("git status %s: %w", path, err)
	}
	return ParseDirtyStatus(string(out)), nil
}

// BuildFetchCmd assembles the `git fetch` for a worktree. Token forges (ado,
// github, forgejo) run through `direnv exec <path> git -c <helper> …` when
// direnv is available so the helper reads the token from the repo's .envrc;
// otherwise plain git with the same helper. GIT_TERMINAL_PROMPT=0 makes a
// missing credential fail fast instead of prompting.
func BuildFetchCmd(ctx context.Context, path, forgeName string, direnvAvailable bool) *exec.Cmd {
	credArgs := gitauth.CredentialArgs(forgeName)
	gitArgs := append(append([]string{}, credArgs...), "-C", path, "fetch", "--quiet")
	var cmd *exec.Cmd
	if credArgs != nil && direnvAvailable {
		cmd = exec.CommandContext(ctx, "direnv", append([]string{"exec", path, "git"}, gitArgs...)...)
	} else {
		cmd = exec.CommandContext(ctx, "git", gitArgs...)
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// HaveDirenv reports whether direnv is on PATH.
func HaveDirenv() bool {
	_, err := exec.LookPath("direnv")
	return err == nil
}

// GitFetch freshens the repo's remote-tracking refs so ahead/behind are
// accurate. One fetch serves all worktrees (shared object store).
func GitFetch(ctx context.Context, path, forgeName string) error {
	if err := BuildFetchCmd(ctx, path, forgeName, HaveDirenv()).Run(); err != nil {
		return fmt.Errorf("git fetch %s: %w", path, err)
	}
	return nil
}
```

Create `internal/navdata/issues.go`:

```go
package navdata

import (
	"context"
	"path/filepath"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// IssueCacheTTL is how long a per-repo issue cache is served without a refetch.
const IssueCacheTTL = 10 * time.Minute

// IssueSource configures issue loading. CacheDir "" disables the cache; Fetch
// nil disables live fetching (cache only).
type IssueSource struct {
	CacheDir string
	Fetch    func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error)
}

// IssueCacheFile is the per-repo cache path, or "" when caching is disabled.
func IssueCacheFile(dir, forgeName, owner, repo string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, forgeName+"_"+owner+"_"+repo+".json")
}

// IssuesCached returns a repo's open issues: a fresh cache wins; otherwise it
// fetches and rewrites the cache. A fetch error (or no fetcher) falls back to
// the last cached slice.
func IssuesCached(ctx context.Context, src IssueSource, forgeName, owner, repo string) []forge.Issue {
	cacheFile := IssueCacheFile(src.CacheDir, forgeName, owner, repo)
	var cached forge.IssueCache
	if cacheFile != "" {
		cached, _ = forge.ReadIssueCache(cacheFile)
		if !cached.UpdatedAt.IsZero() && !cached.IsStale(IssueCacheTTL) {
			return cached.Issues
		}
	}
	if src.Fetch == nil {
		return cached.Issues
	}
	issues, err := src.Fetch(ctx, forgeName, owner, repo)
	if err != nil {
		return cached.Issues
	}
	if cacheFile != "" {
		_ = forge.WriteIssueCache(cacheFile, forge.IssueCache{UpdatedAt: time.Now(), Issues: issues})
	}
	return issues
}
```

- [ ] **Step 4: Run the navdata tests to verify they pass**

Run: `go test -race ./internal/navdata/`
Expected: `ok  	github.com/freaxnx01/bridge/internal/navdata`

- [ ] **Step 5: Make nav's loaders thin wrappers around navdata**

Create `internal/nav/navdata_adapt.go`:

```go
package nav

import "github.com/freaxnx01/bridge/internal/navdata"

// Entry exposes a picker row to navdata's generic list helpers.
func (r repoRow) Entry() navdata.RepoEntry {
	return navdata.RepoEntry{Label: r.label, Repo: r.repo, Remote: r.remote}
}

// withLabel returns a copy of r with a new label (navdata.DisambiguateOwners).
func (r repoRow) withLabel(label string) repoRow {
	r.label = label
	return r
}

// toRepoRows converts navdata entries to picker rows (issue counts pending).
func toRepoRows(es []navdata.RepoEntry) []repoRow {
	if es == nil {
		return nil
	}
	rows := make([]repoRow, 0, len(es))
	for _, e := range es {
		rows = append(rows, repoRow{label: e.Label, repo: e.Repo, remote: e.Remote})
	}
	return rows
}

// fromDashRow converts a navdata row; git status starts pending.
func fromDashRow(d navdata.DashRow) dashRow {
	return dashRow{
		worktree:     d.Worktree,
		branch:       d.Branch,
		path:         d.Path,
		agent:        d.Agent,
		slotID:       d.SlotID,
		state:        d.State,
		lastAccessed: d.LastAccessed,
		hasSession:   d.HasSession,
		displayLabel: d.DisplayLabel,
		isBase:       d.IsBase,
		dirtyState:   loadPending,
	}
}

func fromDashRows(ds []navdata.DashRow) []dashRow {
	rows := make([]dashRow, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, fromDashRow(d))
	}
	return rows
}

func toDirtyInfo(d navdata.Dirty) dirtyInfo {
	return dirtyInfo{modified: d.Modified, ahead: d.Ahead, behind: d.Behind, noUpstream: d.NoUpstream, clean: d.Clean}
}

func fromSessionRows(rs []navdata.SessionRow) []sessionRow {
	rows := make([]sessionRow, 0, len(rs))
	for _, r := range rs {
		rows = append(rows, sessionRow{
			slotID: r.SlotID, repoLabel: r.RepoLabel, worktree: r.Worktree,
			agent: r.Agent, state: r.State, lastAccessed: r.LastAccessed,
		})
	}
	return rows
}
```

In `internal/nav/data.go`, replace these function bodies (keep each function's doc comment):

```go
func loadLocalReposCmd(roots []string) tea.Cmd {
	return func() tea.Msg {
		return reposMsg{rows: toRepoRows(navdata.LocalRepos(context.Background(), roots))}
	}
}

func loadRecentCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return recentMsg{paths: navdata.RecentPaths(path)}
	}
}

func buildSessionRows(live []core.Session, slots []core.Slot, now time.Time) []sessionRow {
	return fromSessionRows(navdata.SessionRows(live, slots, now))
}

func loadRemoteCmd(cachePath string) tea.Cmd {
	return func() tea.Msg {
		es, err := navdata.RemoteRepos(cachePath)
		if err != nil {
			return remoteErrMsg{err: err}
		}
		return remoteMsg{rows: toRepoRows(es)}
	}
}

func remoteRows(refs []forge.RepoRef) []repoRow {
	return toRepoRows(navdata.RemoteEntries(refs))
}

func loadDashRowsCmd(b launcher.Backend, repo core.Repo, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		return dashRowsMsg{rows: fromDashRows(navdata.DashRows(context.Background(), b.Live, repo, slotsPath))}
	}
}

func gitDirtyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		d, err := navdata.GitDirty(context.Background(), path)
		if err != nil {
			return dirtyMsg{path: path, err: err}
		}
		return dirtyMsg{path: path, info: toDirtyInfo(d)}
	}
}

func gitFetchCmd(path, forgeName string) tea.Cmd {
	return func() tea.Msg {
		return fetchDoneMsg{err: navdata.GitFetch(context.Background(), path, forgeName)}
	}
}

func buildFetchCmd(path, forgeName string, direnvAvailable bool) *exec.Cmd {
	return navdata.BuildFetchCmd(context.Background(), path, forgeName, direnvAvailable)
}

func fetchIssuesCached(cfg Config, forgeName, owner, repo string) []forge.Issue {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return navdata.IssuesCached(ctx, navdata.IssueSource{CacheDir: cfg.IssueCacheDir, Fetch: cfg.FetchIssues}, forgeName, owner, repo)
}
```

Then in `internal/nav/data.go` delete (now in navdata, and unused here): `haveDirenv`, `repoLabel`, `remoteLabel`, `const issueCacheTTL`, `issueCacheFile`. Its import block becomes:

```go
import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/launcher"
	"github.com/freaxnx01/bridge/internal/navdata"
	"github.com/freaxnx01/bridge/internal/worktree"
)
```

In `internal/nav/format.go`, replace these bodies (keep the doc comments):

```go
func lastAccessedFor(sess core.Session, now time.Time) string {
	return navdata.LastAccessed(sess, now)
}

func humanLastAccessed(d time.Duration) string { return navdata.HumanLastAccessed(d) }

func dedupRemoteRows(local, remote []repoRow) []repoRow {
	return navdata.DedupRemote(local, remote)
}

func rowParts(r repoRow) (forge, owner, vis, name string) {
	return navdata.Parts(r.Entry())
}

func disambiguateOwners(rows []repoRow) []repoRow {
	return navdata.DisambiguateOwners(rows, repoRow.withLabel)
}

func matchesAllTerms(labelLower string, terms []string) bool {
	return navdata.MatchesAllTerms(labelLower, terms)
}

func filterRepos(rows []repoRow, q string) []repoRow { return navdata.FilterRows(rows, q) }

func matchesForge(r repoRow, forge string) bool { return navdata.MatchesForge(r.Entry(), forge) }

func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
	return fromDashRows(navdata.BuildDashRows(repo, baseBranch, wts, slots, sessions, now))
}

func parseDirtyStatus(out string) dirtyInfo { return toDirtyInfo(navdata.ParseDirtyStatus(out)) }

func sortRepoRows(rows []repoRow) { navdata.SortRows(rows) }
```

and delete these functions with their doc comments (moved to navdata): `repoRowKey`, `ownerQualifiedLabel`, `slotRepoMatches`, `baseRow`, `sortDashRows`, `trackToken`, `repoSortKey`. `parseBranches`, `parseCommits`, `parseStatusFiles` and `windowAround` stay untouched. Its import block becomes:

```go
import (
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/navdata"
	"github.com/freaxnx01/bridge/internal/worktree"
)
```

In `internal/nav/types.go` delete the `forgeOptOrder` variable and its comment (the `forgeOpt` type stays). In `internal/nav/update.go` add the import `"github.com/freaxnx01/bridge/internal/navdata"` and replace the tail of `presentForges`:

```go
	opts := navdata.ForgeOptions()
	out := make([]forgeOpt, 0, len(opts))
	for _, o := range opts {
		if present[o.Key] {
			out = append(out, forgeOpt{key: o.Key, label: o.Label})
		}
	}
	return out
```

- [ ] **Step 6: Run nav's existing tests (unchanged) and the static checks**

Run: `gofmt -l internal/ && go vet ./internal/nav/ ./internal/navdata/ && go test -race ./internal/nav/ ./internal/navdata/ && GOOS=windows go build ./...`
Expected: no gofmt output, then `ok  	github.com/freaxnx01/bridge/internal/nav` and `ok  	github.com/freaxnx01/bridge/internal/navdata`. `git diff --stat -- 'internal/nav/*_test.go'` must print nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/navdata internal/nav/navdata_adapt.go internal/nav/data.go internal/nav/format.go internal/nav/types.go internal/nav/update.go
git commit -m "refactor(nav): extract data loaders into internal/navdata"
```

---

### Task 2: `internal/navdata` — mutations and issue naming

**Files:**
- Create: `internal/navdata/mutate.go`, `internal/navdata/issuename.go`
- Create: `internal/navdata/mutate_test.go`, `internal/navdata/issuename_test.go`
- Modify: `internal/nav/issues.go` (whole file), `internal/nav/data.go` (`createWorktreeCmd`, `createRepoCmd`), `internal/nav/types.go` (`repoForgeChoices`), `internal/nav/view.go` (two `ch.label`), `internal/nav/update.go` (`worktreeErrMessage`)

**Interfaces:**
- Consumes (Task 1): `navdata.DashRow`; nav's `fromDashRow(d navdata.DashRow) dashRow` from `internal/nav/navdata_adapt.go`; the test helper `gitRepo(t *testing.T) string` in `internal/navdata/dash_test.go`.
- Produces (package `navdata`):
  - `type CloneFunc func(ctx context.Context, ref forge.RepoRef) (core.Repo, error)`
  - `type CreateRepoFunc func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error)`
  - `type RepoChoice struct { Label string; Forge string; Private bool }`, `func RepoChoices() []RepoChoice` (order: Forgejo·Private, Forgejo·Public, GitHub·Private, GitHub·Public)
  - `func CreateWorktree(ctx context.Context, repo core.Repo, name, label string) (DashRow, error)`
  - `func WorktreeErrMessage(err error) string`
  - `const IssueSlugMax = 32`, `const IssueLabelMax = 40`
  - `func IssueWorktreeName(number int, title string) (wt, label string)`, `func Slugify(s string) string`, `func TruncRunes(s string, n int) string`

- [ ] **Step 1: Write the failing tests**

Create `internal/navdata/mutate_test.go`:

```go
package navdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/worktree"
)

func TestCreateWorktree_CreatesAndIsIdempotent(t *testing.T) {
	dir := gitRepo(t)
	repo := core.Repo{Name: "solo", Path: dir}
	row, err := CreateWorktree(context.Background(), repo, "feat", "#1 [x]")
	if err != nil {
		t.Fatal(err)
	}
	want := DashRow{Worktree: "feat", Branch: "worktree-feat", Path: filepath.Join(dir, ".worktrees", "feat"), DisplayLabel: "#1 [x]"}
	if row != want {
		t.Errorf("row = %+v, want %+v", row, want)
	}
	again, err := CreateWorktree(context.Background(), repo, "feat", "")
	if err != nil || again.Path != want.Path {
		t.Errorf("second call = %+v, %v; want the same path", again, err)
	}
}

func TestCreateWorktree_ExistingPlainDir_FriendlyMessage(t *testing.T) {
	dir := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".worktrees", "taken", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := CreateWorktree(context.Background(), core.Repo{Name: "solo", Path: dir}, "taken", "")
	var wex *worktree.WorktreeExistsError
	if !errors.As(err, &wex) {
		t.Fatalf("err = %v, want WorktreeExistsError", err)
	}
	if msg := WorktreeErrMessage(err); !strings.Contains(msg, `worktree "taken" already exists`) {
		t.Errorf("message = %q", msg)
	}
}

func TestCreateWorktree_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateWorktree(ctx, core.Repo{Path: t.TempDir()}, "x", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRepoChoices_Order(t *testing.T) {
	got := RepoChoices()
	if len(got) != 4 || got[0] != (RepoChoice{"Forgejo · Private", "forgejo", true}) || got[3] != (RepoChoice{"GitHub · Public", "github", false}) {
		t.Errorf("choices = %+v", got)
	}
}
```

Create `internal/navdata/issuename_test.go` (the first four cases are nav's existing `TestIssueWorktreeName` cases — behaviour must not change for them):

```go
package navdata

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssueWorktreeName(t *testing.T) {
	cases := []struct {
		num       int
		title     string
		wantWT    string
		wantLabel string
	}{
		{127, "show open forge issues", "127-show-open-forge-issues", "#127 [show open forge issues]"},
		{5, "feat(nav): Ctrl+N new repo", "5-feat-nav-ctrl-n-new-repo", "#5 [feat(nav): Ctrl+N new repo]"},
		{42, "", "42", "#42"},
		{9, "!!!", "9", "#9 [!!!]"},
		// Review Focus: emoji and slashes never reach the worktree/branch name.
		{7, "🚀 fix/launch: crash", "7-fix-launch-crash", "#7 [🚀 fix/launch: crash]"},
		{8, "a\nb", "8-a-b", "#8 [a b]"},
	}
	for _, c := range cases {
		wt, label := IssueWorktreeName(c.num, c.title)
		if wt != c.wantWT || label != c.wantLabel {
			t.Errorf("IssueWorktreeName(%d, %q) = %q, %q; want %q, %q", c.num, c.title, wt, label, c.wantWT, c.wantLabel)
		}
	}
}

// Review Focus: a long emoji title must not be cut mid-rune — Telegram rejects
// invalid UTF-8 and tmux/claude would show mojibake.
func TestIssueWorktreeName_LongEmojiTitleStaysValidUTF8(t *testing.T) {
	wt, label := IssueWorktreeName(3, strings.Repeat("🚀", 60))
	if wt != "3" {
		t.Errorf("wt = %q, want %q", wt, "3")
	}
	if !utf8.ValidString(label) {
		t.Fatalf("label is not valid UTF-8: %q", label)
	}
	if n := utf8.RuneCountInString(label); n > len("#3 []")+IssueLabelMax {
		t.Errorf("label has %d runes, want <= %d", n, len("#3 []")+IssueLabelMax)
	}
}

func TestIssueWorktreeName_LongTitleTruncatesSlug(t *testing.T) {
	wt, _ := IssueWorktreeName(1, strings.Repeat("ab cd ", 20))
	if len(wt) > len("1-")+IssueSlugMax || strings.HasSuffix(wt, "-") {
		t.Errorf("slug = %q", wt)
	}
}

func TestTruncRunes(t *testing.T) {
	if got := TruncRunes("héllo wörld", 6); got != "héllo…" {
		t.Errorf("got %q", got)
	}
	if got := TruncRunes("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := TruncRunes("abc", 1); got != "…" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/navdata/`
Expected: FAIL — `undefined: CreateWorktree`, `undefined: IssueWorktreeName`, `undefined: RepoChoices`, `undefined: TruncRunes`.

- [ ] **Step 3: Implement**

Create `internal/navdata/mutate.go`:

```go
package navdata

import (
	"context"
	"errors"
	"fmt"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/worktree"
)

// CloneFunc clones a remote ref and returns the local repo. The cmd layer
// supplies it (direnv-loaded credentials live there, not here).
type CloneFunc func(ctx context.Context, ref forge.RepoRef) (core.Repo, error)

// CreateRepoFunc creates a repo on forgeName (forgejo|github) at the given
// visibility, clones it, and returns the local repo. Supplied by the cmd layer.
type CreateRepoFunc func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error)

// RepoChoice is one forge × visibility option of the new-repo flow.
type RepoChoice struct {
	Label   string
	Forge   string
	Private bool
}

// RepoChoices are the new-repo options in display order.
func RepoChoices() []RepoChoice {
	return []RepoChoice{
		{"Forgejo · Private", "forgejo", true},
		{"Forgejo · Public", "forgejo", false},
		{"GitHub · Private", "github", true},
		{"GitHub · Public", "github", false},
	}
}

// CreateWorktree resolves worktree name of repo, creating
// <repo>/.worktrees/<name> on branch worktree-<name> when it does not exist,
// and returns the row to launch. label overrides the session display name
// ("" keeps "<repo> [<name>]"). worktree.Resolve's error is returned as is: it
// already names the git step, and nav shows its text verbatim.
func CreateWorktree(ctx context.Context, repo core.Repo, name, label string) (DashRow, error) {
	if err := ctx.Err(); err != nil {
		return DashRow{}, err
	}
	dir, _, err := worktree.Resolve(worktree.ExecRunner{}, repo.Path, name)
	if err != nil {
		return DashRow{}, err
	}
	return DashRow{Worktree: name, Branch: "worktree-" + name, Path: dir, DisplayLabel: label}, nil
}

// WorktreeErrMessage maps a CreateWorktree error to a user-facing line: a
// friendly hint for "target dir already exists", the raw text otherwise.
func WorktreeErrMessage(err error) string {
	var wex *worktree.WorktreeExistsError
	if errors.As(err, &wex) {
		return fmt.Sprintf("worktree %q already exists — pick a different name, or remove .worktrees/%s", wex.Name, wex.Name)
	}
	return err.Error()
}
```

Create `internal/navdata/issuename.go`:

```go
package navdata

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// IssueSlugMax bounds the title part of an issue worktree slug.
	IssueSlugMax = 32
	// IssueLabelMax bounds (in runes) the title shown in the session label.
	IssueLabelMax = 40
)

// IssueWorktreeName derives the worktree basename "<number>-<title-slug>" and
// the session label "#<number> [<short title>]" for an issue. The slug is
// ASCII-only (any other rune, including "/" and emoji, is a separator), so it
// is always a safe directory and branch name; the label is truncated on rune
// boundaries, so it is always valid UTF-8. A blank or unslugifiable title
// degrades to just the number ("123" / "#123").
func IssueWorktreeName(number int, title string) (wt, label string) {
	slug := Slugify(title)
	if len(slug) > IssueSlugMax {
		slug = strings.Trim(slug[:IssueSlugMax], "-")
	}
	wt = strconv.Itoa(number)
	if slug != "" {
		wt += "-" + slug
	}
	short := TruncRunes(strings.Join(strings.Fields(title), " "), IssueLabelMax)
	if short == "" {
		return wt, fmt.Sprintf("#%d", number)
	}
	return wt, fmt.Sprintf("#%d [%s]", number, short)
}

// Slugify lowercases s and collapses every run of non-[a-z0-9] runes into one
// hyphen, trimming leading/trailing hyphens.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// TruncRunes shortens s to at most n runes, ending in "…" when cut. It never
// splits a multi-byte rune, so the result is valid UTF-8 whenever s is.
func TruncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
```

- [ ] **Step 4: Run the navdata tests to verify they pass**

Run: `go test -race ./internal/navdata/`
Expected: `ok  	github.com/freaxnx01/bridge/internal/navdata`

- [ ] **Step 5: Point nav at the shared mutations**

Replace `internal/nav/issues.go` entirely with:

```go
package nav

import "github.com/freaxnx01/bridge/internal/navdata"

// issueSlugMax bounds the title portion of an issue-derived worktree slug.
const issueSlugMax = navdata.IssueSlugMax

// issueWorktreeName derives the worktree basename "<number>-<title-slug>" and
// the session label "#<number> [<short title>]" for an issue (see
// navdata.IssueWorktreeName, shared with `bridge bot`).
func issueWorktreeName(number int, title string) (wt, label string) {
	return navdata.IssueWorktreeName(number, title)
}
```

(nav's `issueLabelMax` and `slugify` disappear; `issueSlugMax` stays because `update_test.go` uses it.)

In `internal/nav/data.go` replace the body of `createWorktreeCmd` and the `create(...)` call in `createRepoCmd`:

```go
func createWorktreeCmd(repo core.Repo, name, label string) tea.Cmd {
	return func() tea.Msg {
		row, err := navdata.CreateWorktree(context.Background(), repo, name, label)
		if err != nil {
			return wtCreatedMsg{err: err}
		}
		return wtCreatedMsg{row: fromDashRow(row)}
	}
}
```

```go
		repo, err := create(name, ch.Forge, ch.Private)
```

and remove the now-unused `"github.com/freaxnx01/bridge/internal/worktree"` import from `data.go`.

In `internal/nav/types.go` add the import `"github.com/freaxnx01/bridge/internal/navdata"` and replace the `repoForgeChoices` declaration (the anonymous struct slice) with:

```go
// repoForgeChoices are the forge×visibility options in display order (shared
// with `bridge bot` via navdata). Read-only.
var repoForgeChoices = navdata.RepoChoices()
```

In `internal/nav/view.go` (`viewRepoModal`) change both `ch.label` to `ch.Label`.

In `internal/nav/update.go` replace `worktreeErrMessage` (keep its doc comment) and drop the now-unused `"errors"` and `".../internal/worktree"` imports:

```go
func worktreeErrMessage(err error) string { return navdata.WorktreeErrMessage(err) }
```

- [ ] **Step 6: Run nav's existing tests (unchanged) and the static checks**

Run: `gofmt -l internal/ && go vet ./internal/nav/ ./internal/navdata/ && go test -race ./internal/nav/ ./internal/navdata/ && GOOS=windows go build ./...`
Expected: no gofmt output; `ok` for both packages (`TestIssueWorktreeName`, `TestIssueWorktreeName_LongTitleTruncatesSlug` and the worktree-exists modal test in nav still pass). `git diff --stat -- 'internal/nav/*_test.go'` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/navdata internal/nav/issues.go internal/nav/data.go internal/nav/types.go internal/nav/view.go internal/nav/update.go
git commit -m "refactor(nav): move worktree creation and issue naming into navdata"
```

---

### Task 3: `internal/launch` — resolution and detached start out of `cmd/bridge`

**Files:**
- Create: `internal/launch/launch.go`, `internal/launch/detached.go` (`//go:build !windows`)
- Create: `internal/launch/launch_test.go`, `internal/launch/detached_test.go` (`//go:build !windows`)
- Modify: `cmd/bridge/launch_target.go` (whole file), `cmd/bridge/launch.go` (whole file), `cmd/bridge/preflight.go`, `cmd/bridge/open.go`
- Test (must stay green unchanged): `cmd/bridge/launch_test.go`, `cmd/bridge/launch_target_test.go`, `cmd/bridge/preflight_test.go`, `cmd/bridge/preflight_worktree_test.go`, `cmd/bridge/keyword_test.go`

**Interfaces:**
- Consumes: `agents.Resolve`, `agents.AgentSpec`; `core.Repo`, `core.Session`, `core.Slot`, `core.SlotID`, `core.UpsertSlot`; `hooks.EnsureRelabel`, `hooks.EffectiveConfigDir`; `store.MRUTouch`; `syncer.Syncer`, `syncer.ExecRunner`, `syncer.SkipNoUpstream`; `worktree.Resolve`; `launcher.Tmux{}.LaunchArgvDetached(slot, dir string, agent agents.AgentSpec) ([]string, error)`. In `cmd/bridge`: `reposWithMeta() ([]core.Repo, error)`, `cacheRoot() string`, `loadSessions() ([]core.Session, error)`.
- Produces (package `github.com/freaxnx01/bridge/internal/launch`):
  - `type Deps struct { Repos func() ([]core.Repo, error); CacheRoot string; Live func() ([]core.Session, error); Getenv func(string) string; Stderr io.Writer; Exec func(ctx context.Context, argv []string) ([]byte, error); PollEvery time.Duration }`
  - `type Target struct { Repo core.Repo; Worktree, WorkDir string; WorktreeCreated bool; Slot, AgentName string; Spec agents.AgentSpec; HasAgent bool; Label string }`
  - `type RepoLookupError struct{ Msg string }`, `type AgentLookupError struct{ Cause error }` (Error() = `"bridge: " + Cause.Error()`, Unwrap), `type RCAgentError struct{ Agent string }` (Error() = `"bridge: --rc needs the claude agent (got <agent>)"`)
  - `func FindRepoByName(repos []core.Repo, name string) (core.Repo, bool)`, `func FindReposByKeyword(repos []core.Repo, q string) []core.Repo`
  - `func DefaultAgent(getenv func(string) string) (agents.AgentSpec, bool)`, `func DisplayName(repo core.Repo, worktree string) string`, `func WithClaudeName(spec agents.AgentSpec, label string) agents.AgentSpec`, `func EnsureRelabel(spec agents.AgentSpec, label string, stderr io.Writer)`
  - `func ResolveTarget(ctx context.Context, d Deps, name, worktree, agentName string) (Target, error)`
  - `func TargetFor(ctx context.Context, d Deps, repo core.Repo, worktree, agentName string) (Target, error)`
  - `func Finalize(ctx context.Context, d Deps, t *Target, noSync bool)`, `func PreLaunchSync(ctx context.Context, d Deps, dir, slot string, noSync bool)`, `func SessionLive(d Deps, slot string) bool`
  - unix only: `func StartDetached(ctx context.Context, d Deps, t *Target, rc, noSync bool) (alreadyRunning bool, err error)`, `func WaitRCURL(ctx context.Context, d Deps, slot string, wait time.Duration) string`, `func Stop(ctx context.Context, d Deps, slot string) error`
  - In `cmd/bridge`: `func launchDeps() launch.Deps`; the aliases `launchTarget = launch.Target`, `errRepoLookup = launch.RepoLookupError`, `errAgentLookup = launch.AgentLookupError` and the wrappers `resolveLaunchTarget`, `finalizeLaunch` keep their names and signatures.

- [ ] **Step 1: Write the failing tests**

Create `internal/launch/launch_test.go`:

```go
package launch

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func reposDeps(repos ...core.Repo) Deps {
	return Deps{
		Repos:  func() ([]core.Repo, error) { return repos, nil },
		Getenv: env(nil),
	}
}

func TestFindReposByKeyword_NameHitsWin(t *testing.T) {
	repos := []core.Repo{{Name: "bridge"}, {Name: "other", Topics: []string{"bridge"}}}
	got := FindReposByKeyword(repos, "brid")
	if len(got) != 1 || got[0].Name != "bridge" {
		t.Errorf("got %+v, want only the name hit", got)
	}
}

func TestDefaultAgent_AppendsArgsWithoutMutatingRegistry(t *testing.T) {
	spec, ok := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude", "BRIDGE_DEFAULT_AGENT_ARGS": "--a  --b"}))
	if !ok || spec.Name != "claude" || !reflect.DeepEqual(spec.Args, []string{"--a", "--b"}) {
		t.Fatalf("spec = %+v, ok = %v", spec, ok)
	}
	again, _ := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude"}))
	if len(again.Args) != 0 {
		t.Errorf("registry spec mutated: %+v", again)
	}
	if _, ok := DefaultAgent(env(nil)); ok {
		t.Error("unset BRIDGE_DEFAULT_AGENT must report ok=false")
	}
}

func TestWithClaudeName(t *testing.T) {
	spec, _ := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude", "BRIDGE_DEFAULT_AGENT_ARGS": "--x"}))
	got := WithClaudeName(spec, "bridge [wt]")
	if !reflect.DeepEqual(got.Args, []string{"-n", "bridge [wt]", "--x"}) {
		t.Errorf("args = %v", got.Args)
	}
	code, _ := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "code"}))
	if got := WithClaudeName(code, "x"); !reflect.DeepEqual(got, code) {
		t.Errorf("non-claude spec changed: %+v", got)
	}
}

func TestResolveTarget_UnknownAndAmbiguous(t *testing.T) {
	d := reposDeps(core.Repo{Name: "alpha", Path: "/a"}, core.Repo{Name: "alpine", Path: "/b"})
	_, err := ResolveTarget(context.Background(), d, "zzz", "", "claude")
	var le RepoLookupError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), `unknown repo "zzz"`) {
		t.Errorf("err = %v, want unknown-repo RepoLookupError", err)
	}
	_, err = ResolveTarget(context.Background(), d, "alp", "", "claude")
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "ambiguous (2 matches): alpha, alpine") {
		t.Errorf("err = %v, want ambiguous RepoLookupError", err)
	}
}

func TestResolveTarget_UnknownAgent(t *testing.T) {
	d := reposDeps(core.Repo{Name: "alpha", Path: t.TempDir()})
	_, err := ResolveTarget(context.Background(), d, "alpha", "", "nope")
	var ae AgentLookupError
	if !errors.As(err, &ae) || !strings.HasPrefix(err.Error(), "bridge: ") {
		t.Errorf("err = %v, want AgentLookupError with bridge: prefix", err)
	}
}

func TestTargetFor_UsesGivenRepoAndDefaultAgent(t *testing.T) {
	d := Deps{Getenv: env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude"})}
	repo := core.Repo{Name: "bridge", Path: t.TempDir(), Forge: "forgejo"}
	tg, err := TargetFor(context.Background(), d, repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Repo.Forge != "forgejo" || tg.Slot != "bridge" || !tg.HasAgent || tg.AgentName != "claude" || tg.WorkDir != repo.Path {
		t.Errorf("target = %+v", tg)
	}
}

func TestTargetFor_NoAgent(t *testing.T) {
	tg, err := TargetFor(context.Background(), Deps{Getenv: env(nil)}, core.Repo{Name: "x", Path: t.TempDir()}, "", "")
	if err != nil || tg.HasAgent {
		t.Errorf("target = %+v, err = %v; want HasAgent=false", tg, err)
	}
}

func TestFinalize_LabelSlotAndMRU(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cache := t.TempDir()
	d := Deps{CacheRoot: cache, Getenv: env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude"})}
	repo := core.Repo{Name: "bridge", Path: t.TempDir()}
	tg, err := TargetFor(context.Background(), d, repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tg.Label = "#7 [fix]"
	Finalize(context.Background(), d, &tg, true)
	if !reflect.DeepEqual(tg.Spec.Args, []string{"-n", "#7 [fix]"}) {
		t.Errorf("args = %v, want the -n label", tg.Spec.Args)
	}
	slots, err := core.LoadSlots(filepath.Join(cache, "slots.json"))
	if err != nil || len(slots) != 1 || slots[0].ID != "bridge" || slots[0].Agent != "claude" {
		t.Errorf("slots = %+v, %v", slots, err)
	}
	mru, _ := core.LoadMRU(filepath.Join(cache, "mru"))
	if len(mru) != 1 || mru[0] != repo.Path {
		t.Errorf("mru = %v, want [%s]", mru, repo.Path)
	}
}

func TestSessionLive(t *testing.T) {
	d := Deps{Live: func() ([]core.Session, error) { return []core.Session{{SlotID: "a"}}, nil }}
	if !SessionLive(d, "a") || SessionLive(d, "b") || SessionLive(Deps{}, "a") {
		t.Error("SessionLive mismatch")
	}
}
```

Create `internal/launch/detached_test.go`:

```go
//go:build !windows

package launch

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
)

// fakeExec records every argv and answers capture-pane with pane.
type fakeExec struct {
	mu     sync.Mutex
	calls  [][]string
	pane   string
	failOn string // argv[1] that fails ("" = none)
}

func (f *fakeExec) run(_ context.Context, argv []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, argv)
	if len(argv) > 1 && argv[1] == f.failOn {
		return []byte("boom"), errors.New("exit status 1")
	}
	if len(argv) > 1 && argv[1] == "capture-pane" {
		return []byte(f.pane), nil
	}
	return nil, nil
}

func liveSlots(ids ...string) func() ([]core.Session, error) {
	return func() ([]core.Session, error) {
		var out []core.Session
		for _, id := range ids {
			out = append(out, core.Session{SlotID: id})
		}
		return out, nil
	}
}

func claudeTarget(t *testing.T) Target {
	spec, _ := agents.Resolve("claude")
	return Target{Repo: core.Repo{Name: "bridge"}, WorkDir: t.TempDir(), Slot: "bridge", AgentName: "claude", Spec: spec, HasAgent: true}
}

func TestStartDetached_NewSessionWithRC(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	fx := &fakeExec{}
	d := Deps{Exec: fx.run, Live: liveSlots(), Getenv: env(nil)}
	tg := claudeTarget(t)
	running, err := StartDetached(context.Background(), d, &tg, true, true)
	if err != nil || running {
		t.Fatalf("running=%v err=%v", running, err)
	}
	if len(fx.calls) != 1 || fx.calls[0][0] != "sh" {
		t.Fatalf("calls = %v, want one sh -c", fx.calls)
	}
	body := fx.calls[0][2]
	if !strings.Contains(body, "new-session -d -s bridge") || !strings.Contains(body, "--remote-control") || !strings.Contains(body, "-n bridge") {
		t.Errorf("body = %s", body)
	}
}

func TestStartDetached_AlreadyRunningTouchesNothing(t *testing.T) {
	fx := &fakeExec{}
	d := Deps{Exec: fx.run, Live: liveSlots("bridge")}
	tg := claudeTarget(t)
	running, err := StartDetached(context.Background(), d, &tg, false, true)
	if err != nil || !running || len(fx.calls) != 0 {
		t.Errorf("running=%v err=%v calls=%v", running, err, fx.calls)
	}
}

func TestStartDetached_RCNeedsClaude(t *testing.T) {
	fx := &fakeExec{}
	spec, _ := agents.Resolve("code")
	tg := Target{Slot: "x", WorkDir: t.TempDir(), Spec: spec}
	_, err := StartDetached(context.Background(), Deps{Exec: fx.run}, &tg, true, true)
	var rc RCAgentError
	if !errors.As(err, &rc) || err.Error() != "bridge: --rc needs the claude agent (got code)" || len(fx.calls) != 0 {
		t.Errorf("err = %v calls = %v", err, fx.calls)
	}
}

func TestStartDetached_TmuxFailureWrapped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	fx := &fakeExec{failOn: "-c"}
	tg := claudeTarget(t)
	_, err := StartDetached(context.Background(), Deps{Exec: fx.run, Getenv: env(nil)}, &tg, false, true)
	if err == nil || !strings.HasPrefix(err.Error(), "tmux: ") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

func TestWaitRCURL_LastMatchWins(t *testing.T) {
	fx := &fakeExec{pane: "old https://claude.ai/code/session_OLD1\nnew https://claude.ai/code/session_NEW2"}
	got := WaitRCURL(context.Background(), Deps{Exec: fx.run, Live: liveSlots("s")}, "s", time.Second)
	if got != "https://claude.ai/code/session_NEW2" {
		t.Errorf("got %q", got)
	}
}

func TestWaitRCURL_SessionGoneReturnsEarly(t *testing.T) {
	fx := &fakeExec{failOn: "capture-pane"}
	start := time.Now()
	got := WaitRCURL(context.Background(), Deps{Exec: fx.run, Live: liveSlots()}, "s", 5*time.Second)
	if got != "" || time.Since(start) > time.Second {
		t.Errorf("got %q after %s; want \"\" immediately", got, time.Since(start))
	}
}

func TestWaitRCURL_IgnoresNonSessionURLAndTimesOut(t *testing.T) {
	fx := &fakeExec{pane: "see https://claude.ai/code/artifact/abc"}
	d := Deps{Exec: fx.run, Live: liveSlots("s"), PollEvery: 10 * time.Millisecond}
	if got := WaitRCURL(context.Background(), d, "s", 50*time.Millisecond); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestStop(t *testing.T) {
	fx := &fakeExec{}
	if err := Stop(context.Background(), Deps{Exec: fx.run}, "bridge-wt-x"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tmux", "kill-session", "-t", "bridge-wt-x"}; !slices.Equal(fx.calls[0], want) {
		t.Errorf("argv = %v, want %v", fx.calls[0], want)
	}
	if err := Stop(context.Background(), Deps{Exec: fx.run}, ""); err == nil {
		t.Error("empty slot must fail")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/launch/`
Expected: FAIL — `undefined: Deps`, `undefined: DefaultAgent`, `undefined: StartDetached`, …

- [ ] **Step 3: Implement `internal/launch`**

Create `internal/launch/launch.go` (logic moved from `cmd/bridge/launch_target.go`, `preflight.go` and `open.go`; messages byte-identical):

```go
// Package launch resolves a repo/worktree/agent into a launch Target and
// starts it — the shared core of `bridge <repo>` (preflight), `bridge launch`
// and `bridge bot`. The cmd layer keeps flag parsing, exit codes and printing.
package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/hooks"
	"github.com/freaxnx01/bridge/internal/store"
	"github.com/freaxnx01/bridge/internal/syncer"
	worktreepkg "github.com/freaxnx01/bridge/internal/worktree"
)

// Deps is everything launch needs from the cmd layer. Zero-value fields fall
// back to safe defaults (see the accessor methods).
type Deps struct {
	// Repos lists the discoverable repos (cmd: reposWithMeta).
	Repos func() ([]core.Repo, error)
	// CacheRoot holds "mru" and "slots.json"; "" skips both writes.
	CacheRoot string
	// Live lists live sessions (cmd: loadSessions, which honours
	// BRIDGE_TMUX_FIXTURE). Nil => nothing is live.
	Live func() ([]core.Session, error)
	// Getenv reads BRIDGE_DEFAULT_AGENT(_ARGS) and BRIDGE_NO_SYNC. Nil => os.Getenv.
	Getenv func(string) string
	// Stderr receives the one-line notes bridge has always printed. Nil => discarded.
	Stderr io.Writer
	// Exec runs argv and returns its combined output. Nil => exec.CommandContext.
	Exec func(ctx context.Context, argv []string) ([]byte, error)
	// PollEvery is WaitRCURL's poll interval. 0 => 500ms.
	PollEvery time.Duration
}

func (d Deps) getenv(k string) string {
	if d.Getenv == nil {
		return os.Getenv(k)
	}
	return d.Getenv(k)
}

func (d Deps) stderr() io.Writer {
	if d.Stderr == nil {
		return io.Discard
	}
	return d.Stderr
}

func (d Deps) run(ctx context.Context, argv []string) ([]byte, error) {
	if d.Exec != nil {
		return d.Exec(ctx, argv)
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

// Target is everything needed to start (or reattach) an agent session.
type Target struct {
	Repo            core.Repo
	Worktree        string
	WorkDir         string
	WorktreeCreated bool
	Slot            string
	AgentName       string
	Spec            agents.AgentSpec
	HasAgent        bool
	// Label overrides the claude display name ("" => DisplayName(Repo, Worktree)).
	// Issue launches set "#123 [<title>]".
	Label string
}

// RepoLookupError marks an unknown or ambiguous repo name (cmd: exit 2).
type RepoLookupError struct{ Msg string }

func (e RepoLookupError) Error() string { return e.Msg }

// AgentLookupError marks an unknown --agent (cmd: exit 2).
type AgentLookupError struct{ Cause error }

func (e AgentLookupError) Error() string { return "bridge: " + e.Cause.Error() }
func (e AgentLookupError) Unwrap() error { return e.Cause }

// RCAgentError marks a Remote Control request for a non-claude agent (cmd: exit 2).
type RCAgentError struct{ Agent string }

func (e RCAgentError) Error() string {
	return fmt.Sprintf("bridge: --rc needs the claude agent (got %s)", e.Agent)
}

// FindRepoByName returns the first repo whose Name equals name (case-insensitive).
func FindRepoByName(repos []core.Repo, name string) (core.Repo, bool) {
	needle := strings.ToLower(name)
	for _, r := range repos {
		if strings.ToLower(r.Name) == needle {
			return r, true
		}
	}
	return core.Repo{}, false
}

// FindReposByKeyword returns repos matching q (case-insensitive substring) in
// Name, else in Desc or a Topic. Name matches win: if any Name contains q,
// only Name matches are returned.
func FindReposByKeyword(repos []core.Repo, q string) []core.Repo {
	needle := strings.ToLower(q)
	var nameHits, metaHits []core.Repo
	for _, r := range repos {
		if strings.Contains(strings.ToLower(r.Name), needle) {
			nameHits = append(nameHits, r)
			continue
		}
		if strings.Contains(strings.ToLower(r.Desc), needle) {
			metaHits = append(metaHits, r)
			continue
		}
		for _, t := range r.Topics {
			if strings.Contains(strings.ToLower(t), needle) {
				metaHits = append(metaHits, r)
				break
			}
		}
	}
	if len(nameHits) > 0 {
		return nameHits
	}
	return metaHits
}

// DefaultAgent resolves BRIDGE_DEFAULT_AGENT, appending the space-split
// BRIDGE_DEFAULT_AGENT_ARGS. ok=false when unset or unknown.
func DefaultAgent(getenv func(string) string) (agents.AgentSpec, bool) {
	name := getenv("BRIDGE_DEFAULT_AGENT")
	if name == "" {
		return agents.AgentSpec{}, false
	}
	spec, err := agents.Resolve(name)
	if err != nil {
		return agents.AgentSpec{}, false
	}
	if extra := getenv("BRIDGE_DEFAULT_AGENT_ARGS"); extra != "" {
		spec.Args = append(append([]string{}, spec.Args...), strings.Fields(extra)...)
	}
	return spec, true
}

// DisplayName is the claude session name: "<repo>" or "<repo> [<worktree>]".
func DisplayName(repo core.Repo, worktree string) string {
	if worktree != "" {
		return repo.Name + " [" + worktree + "]"
	}
	return repo.Name
}

// WithClaudeName prepends `-n <label>` to a claude spec's args (no-op for
// other agents). Builds a fresh Args slice; the registry spec is never mutated.
func WithClaudeName(spec agents.AgentSpec, label string) agents.AgentSpec {
	if spec.Name != "claude" {
		return spec
	}
	spec.Args = append([]string{"-n", label}, spec.Args...)
	return spec
}

// EnsureRelabel installs the SessionStart[clear] hook that restores label
// after /clear (claude only). Failures are a warning on stderr, never fatal.
func EnsureRelabel(spec agents.AgentSpec, label string, stderr io.Writer) {
	if spec.Name != "claude" {
		return
	}
	if err := hooks.EnsureRelabel(hooks.EffectiveConfigDir(), label); err != nil {
		fmt.Fprintf(stderr, "warning: relabel hook install failed: %v\n", err)
	}
}

// ResolveTarget looks name up (exact name first, then keyword) and builds its Target.
func ResolveTarget(ctx context.Context, d Deps, name, worktree, agentName string) (Target, error) {
	if d.Repos == nil {
		return Target{}, errors.New("launch: Deps.Repos is nil")
	}
	repos, err := d.Repos()
	if err != nil {
		return Target{}, fmt.Errorf("launch: list repos: %w", err)
	}
	repo, ok := FindRepoByName(repos, name)
	if !ok {
		matches := FindReposByKeyword(repos, name)
		switch len(matches) {
		case 1:
			repo = matches[0]
		case 0:
			return Target{}, RepoLookupError{fmt.Sprintf("bridge: unknown repo %q", name)}
		default:
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return Target{}, RepoLookupError{fmt.Sprintf("bridge: %q is ambiguous (%d matches): %s",
				name, len(matches), strings.Join(names, ", "))}
		}
	}
	return TargetFor(ctx, d, repo, worktree, agentName)
}

// TargetFor builds the Target for an already-resolved repo (the bot passes the
// exact repo the user tapped, so same-named repos on two forges never mix).
// It touches the MRU and, with a worktree, resolves or creates it; a git
// failure falls back to the bare <repo>/.worktrees/<wt> path. agentName ""
// uses DefaultAgent.
func TargetFor(ctx context.Context, d Deps, repo core.Repo, worktree, agentName string) (Target, error) {
	if err := ctx.Err(); err != nil {
		return Target{}, err
	}
	if d.CacheRoot != "" {
		_ = store.MRUTouch(filepath.Join(d.CacheRoot, "mru"), repo.Path) // best effort, as before
	}
	t := Target{Repo: repo, Worktree: worktree, WorkDir: repo.Path}
	if worktree != "" {
		if dir, created, werr := worktreepkg.Resolve(worktreepkg.ExecRunner{}, repo.Path, worktree); werr == nil {
			t.WorkDir, t.WorktreeCreated = dir, created
			if created {
				fmt.Fprintf(d.stderr(), "bridge: created worktree %s\n", dir)
			}
		} else {
			t.WorkDir = filepath.Join(repo.Path, ".worktrees", worktree)
			fmt.Fprintf(d.stderr(), "bridge: worktree resolve failed (%v); using %s\n", werr, t.WorkDir)
		}
	}
	if agentName != "" {
		spec, err := agents.Resolve(agentName)
		if err != nil {
			return Target{}, AgentLookupError{err}
		}
		t.Spec, t.AgentName, t.HasAgent = spec, agentName, true
	} else if spec, ok := DefaultAgent(d.getenv); ok {
		t.Spec, t.AgentName, t.HasAgent = spec, spec.Name, true
	}
	t.Slot = core.SlotID(repo.Name, worktree)
	return t, nil
}

// Finalize applies the side effects every agent launch shares: the claude
// `-n` label + relabel hook, the best-effort pre-launch sync (skipped for a
// live slot), and the slot-registry upsert (non-fatal).
func Finalize(ctx context.Context, d Deps, t *Target, noSync bool) {
	label := t.Label
	if label == "" {
		label = DisplayName(t.Repo, t.Worktree)
	}
	t.Spec = WithClaudeName(t.Spec, label)
	EnsureRelabel(t.Spec, label, d.stderr())
	PreLaunchSync(ctx, d, t.WorkDir, t.Slot, noSync)
	if d.CacheRoot == "" {
		return
	}
	if err := core.UpsertSlot(filepath.Join(d.CacheRoot, "slots.json"), core.Slot{
		ID:       t.Slot,
		Repo:     t.Repo.Name,
		Worktree: t.Worktree,
		Agent:    t.AgentName,
		Created:  time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(d.stderr(), "warning: slot upsert failed: %v\n", err)
	}
}

// PreLaunchSync does a best-effort `git fetch && git pull --ff-only` in dir
// (issue #90). Skipped when noSync, BRIDGE_NO_SYNC, an empty dir, or a live
// slot (never touch the tree under a running session). Never fails a launch.
func PreLaunchSync(ctx context.Context, d Deps, dir, slot string, noSync bool) {
	if noSync || d.getenv("BRIDGE_NO_SYNC") != "" || dir == "" {
		return
	}
	if slot != "" && SessionLive(d, slot) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s := &syncer.Syncer{Runner: syncer.ExecRunner{}}
	res := s.SafePull(ctx, dir)
	if res.Skipped != "" && res.Skipped != syncer.SkipNoUpstream {
		fmt.Fprintf(d.stderr(), "bridge: sync skipped (%s)\n", res.Skipped)
	}
}

// SessionLive reports whether slot is among d.Live's sessions.
func SessionLive(d Deps, slot string) bool {
	if d.Live == nil {
		return false
	}
	sessions, err := d.Live()
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if s.SlotID == slot {
			return true
		}
	}
	return false
}
```

Create `internal/launch/detached.go` (moved from `cmd/bridge/launch.go`):

```go
//go:build !windows

package launch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/launcher"
)

// rcURLPattern matches the Remote Control link claude prints in its pane.
// Compiled once; *regexp.Regexp is immutable and safe for concurrent use.
var rcURLPattern = regexp.MustCompile(`https://claude\.ai/code/session_[A-Za-z0-9]+`)

// StartDetached starts t in a detached tmux session unless its slot is
// already live (alreadyRunning=true, nothing touched). With rc it requires
// the claude agent (RCAgentError otherwise) and adds --remote-control.
// Finalize runs only for a new session.
func StartDetached(ctx context.Context, d Deps, t *Target, rc, noSync bool) (alreadyRunning bool, err error) {
	if rc {
		if t.Spec.Name != "claude" {
			return false, RCAgentError{Agent: t.Spec.Name}
		}
		if !slices.Contains(t.Spec.Args, "--remote-control") {
			t.Spec.Args = append(slices.Clone(t.Spec.Args), "--remote-control")
		}
	}
	if SessionLive(d, t.Slot) {
		return true, nil
	}
	Finalize(ctx, d, t, noSync)
	argv, err := launcher.Tmux{}.LaunchArgvDetached(t.Slot, t.WorkDir, t.Spec)
	if err != nil {
		return false, fmt.Errorf("launch: %w", err)
	}
	if out, err := d.run(ctx, argv); err != nil {
		return false, fmt.Errorf("tmux: %w: %s", err, out)
	}
	return false, nil
}

// WaitRCURL polls slot's pane for the Remote Control link for up to wait.
// Best effort: "" on timeout, on ctx end, or as soon as the session is gone.
// The last match wins (scrollback may hold links from an earlier run).
func WaitRCURL(ctx context.Context, d Deps, slot string, wait time.Duration) string {
	poll := d.PollEvery
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		out, err := d.run(ctx, []string{"tmux", "capture-pane", "-p", "-J", "-t", slot, "-S", "-200"})
		if err != nil {
			if !SessionLive(d, slot) {
				return ""
			}
		} else if ms := rcURLPattern.FindAllString(string(out), -1); len(ms) > 0 {
			return ms[len(ms)-1]
		}
		if time.Now().After(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(poll):
		}
	}
}

// Stop kills slot's tmux session.
func Stop(ctx context.Context, d Deps, slot string) error {
	if slot == "" {
		return errors.New("launch: empty slot")
	}
	if out, err := d.run(ctx, []string{"tmux", "kill-session", "-t", slot}); err != nil {
		return fmt.Errorf("tmux kill-session %s: %w: %s", slot, err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 4: Run the launch tests to verify they pass**

Run: `go test -race ./internal/launch/ && GOOS=windows go build ./internal/launch/`
Expected: `ok  	github.com/freaxnx01/bridge/internal/launch`, then no output from the Windows build.

- [ ] **Step 5: Make `cmd/bridge` delegate**

Replace `cmd/bridge/launch_target.go` entirely with:

```go
package main

import (
	"context"
	"os"

	"github.com/freaxnx01/bridge/internal/launch"
)

// The launch logic lives in internal/launch (shared with `bridge bot`). These
// cmd-layer names stay so preflight, `bridge launch` and their tests read
// unchanged.
type (
	// launchTarget is everything needed to start (or reattach) a session.
	launchTarget = launch.Target
	// errRepoLookup marks unknown/ambiguous repo names; callers print it and exit 2.
	errRepoLookup = launch.RepoLookupError
	// errAgentLookup marks an unknown/invalid --agent; callers print it and exit 2.
	errAgentLookup = launch.AgentLookupError
)

// launchDeps wires internal/launch to this binary's discovery, cache dir,
// session listing (BRIDGE_TMUX_FIXTURE-aware) and stderr notes.
func launchDeps() launch.Deps {
	return launch.Deps{
		Repos:     reposWithMeta,
		CacheRoot: cacheRoot(),
		Live:      loadSessions,
		Getenv:    os.Getenv,
		Stderr:    os.Stderr,
	}
}

func resolveLaunchTarget(name, worktree, agentName string) (launchTarget, error) {
	return launch.ResolveTarget(context.Background(), launchDeps(), name, worktree, agentName)
}

// finalizeLaunch applies the shared launch side effects (label, relabel hook,
// pre-launch sync, slot upsert); see launch.Finalize.
func finalizeLaunch(t *launchTarget, noSync bool) {
	launch.Finalize(context.Background(), launchDeps(), t, noSync)
}
```

Replace `cmd/bridge/launch.go` entirely with (flags, `launchResult` and output are unchanged; `rcURLPattern` and `waitRCURL` are gone — they live in `internal/launch`):

```go
//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/launch"
)

var (
	launchWorktree string
	launchAgent    string
	launchRC       bool
	launchRCWait   time.Duration
	launchNoSync   bool
	launchJSON     bool
)

// launchResult is `bridge launch --json` output.
type launchResult struct {
	Slot            string `json:"slot"`
	Repo            string `json:"repo"`
	Worktree        string `json:"worktree,omitempty"`
	Dir             string `json:"dir"`
	Agent           string `json:"agent"`
	WorktreeCreated bool   `json:"worktree_created"`
	AlreadyRunning  bool   `json:"already_running"`
	RCURL           string `json:"rc_url,omitempty"`
}

var launchCmd = &cobra.Command{
	Use:   "launch <repo>",
	Short: "Start an agent session in the background (no attach) — for scripts and the Telegram admin session",
	Long: `launch resolves <repo> (and -w worktree) exactly like ` + "`bridge <repo>`" + `, but
creates the tmux session detached and returns instead of attaching. Idempotent:
a live slot is reported as already_running and left untouched. With --rc the
Remote Control URL is scraped from the pane (best effort, up to --rc-wait).`,
	Args:              cobra.ExactArgs(1),
	RunE:              runLaunch,
	ValidArgsFunction: completeRepoName,
}

func init() {
	launchCmd.Flags().StringVarP(&launchWorktree, "worktree", "w", "", "worktree name (created under .worktrees/ if missing)")
	launchCmd.Flags().StringVar(&launchAgent, "agent", "", "agent (claude|copilot|opencode|code); default BRIDGE_DEFAULT_AGENT")
	launchCmd.Flags().BoolVar(&launchRC, "rc", false, "enable Remote Control (claude only) and report its URL")
	launchCmd.Flags().DurationVar(&launchRCWait, "rc-wait", 20*time.Second, "how long to wait for the Remote Control URL")
	launchCmd.Flags().BoolVar(&launchNoSync, "no-sync", false, "skip the pre-launch git pull")
	launchCmd.Flags().BoolVar(&launchJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(launchCmd)
}

func runLaunch(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	fail := func(format string, a ...any) error {
		fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
		os.Exit(2)
		return nil
	}
	// Validate before resolveLaunchTarget: it creates worktrees and touches the MRU.
	if launchRC && launchAgent != "" && launchAgent != "claude" {
		return fail("bridge: --rc needs the claude agent (got %s)", launchAgent)
	}
	if launchAgent == "" && os.Getenv("BRIDGE_DEFAULT_AGENT") == "" {
		return fail("bridge: no agent — pass --agent or set BRIDGE_DEFAULT_AGENT")
	}
	t, err := resolveLaunchTarget(args[0], launchWorktree, launchAgent)
	if err != nil {
		var repoErr errRepoLookup
		var agentErr errAgentLookup
		if errors.As(err, &repoErr) || errors.As(err, &agentErr) {
			return fail("%v", err)
		}
		return err
	}
	if !t.HasAgent {
		return fail("bridge: no agent — pass --agent or set BRIDGE_DEFAULT_AGENT")
	}
	ctx := context.Background()
	d := launchDeps()
	running, err := launch.StartDetached(ctx, d, &t, launchRC, launchNoSync)
	if err != nil {
		var rcErr launch.RCAgentError
		if errors.As(err, &rcErr) {
			return fail("%v", err)
		}
		return err
	}

	res := launchResult{
		Slot: t.Slot, Repo: t.Repo.Name, Worktree: t.Worktree, Dir: t.WorkDir,
		Agent: t.AgentName, WorktreeCreated: t.WorktreeCreated, AlreadyRunning: running,
	}
	if launchRC {
		res.RCURL = launch.WaitRCURL(ctx, d, t.Slot, launchRCWait)
		if res.RCURL == "" {
			if !launch.SessionLive(d, t.Slot) {
				fmt.Fprintf(cmd.ErrOrStderr(), "bridge: session %s exited during startup\n", t.Slot)
				os.Exit(1)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "bridge: no Remote Control URL seen within %s (session is running)\n", launchRCWait)
		}
	}
	if launchJSON {
		return emitJSON(cmd.OutOrStdout(), res)
	}
	state := "launched"
	if running {
		state = "already running"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", state, t.Slot, t.WorkDir)
	if res.RCURL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "remote control: %s\n", res.RCURL)
	}
	return nil
}
```

In `cmd/bridge/preflight.go` replace these bodies (keep the doc comments), delete `sessionLive` (no caller left), and swap the imports `internal/hooks` + `internal/syncer` for `internal/launch`:

```go
func resolveDefaultAgent() (agents.AgentSpec, bool) { return launch.DefaultAgent(os.Getenv) }

func displayName(repo core.Repo, worktree string) string { return launch.DisplayName(repo, worktree) }

func withClaudeNameLabel(spec agents.AgentSpec, label string) agents.AgentSpec {
	return launch.WithClaudeName(spec, label)
}

func maybePreLaunchSync(dir, slot string, noSync bool) {
	launch.PreLaunchSync(context.Background(), launchDeps(), dir, slot, noSync)
}

func ensureClaudeRelabelLabel(spec agents.AgentSpec, label string) {
	launch.EnsureRelabel(spec, label, os.Stderr)
}
```

The resulting import block of `preflight.go`:

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/launch"
	"github.com/freaxnx01/bridge/internal/launcher"
	"github.com/freaxnx01/bridge/internal/shellbridge"
	"github.com/freaxnx01/bridge/internal/store"
)
```

In `cmd/bridge/open.go` replace the two lookup bodies (keep the doc comments), drop the `"strings"` import and add `"github.com/freaxnx01/bridge/internal/launch"`:

```go
func findRepoByName(repos []core.Repo, name string) (core.Repo, bool) {
	return launch.FindRepoByName(repos, name)
}

func findReposByKeyword(repos []core.Repo, q string) []core.Repo {
	return launch.FindReposByKeyword(repos, q)
}
```

- [ ] **Step 6: Run the full `cmd/bridge` suite (existing tests unchanged) and checks**

Run: `gofmt -l cmd internal && go vet ./... && go test -race ./cmd/bridge/ ./internal/launch/ && GOOS=windows go build ./...`
Expected: no gofmt output; `ok  	github.com/freaxnx01/bridge/cmd/bridge` (incl. `TestLaunch*`, `TestResolveLaunchTarget*`, `TestLaunchRejectsBeforeSideEffects`, the preflight tests) and `ok` for `internal/launch`. `git diff --stat -- 'cmd/bridge/*_test.go'` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/launch cmd/bridge/launch_target.go cmd/bridge/launch.go cmd/bridge/preflight.go cmd/bridge/open.go
git commit -m "refactor(launch): move launch resolution and detached start into internal/launch"
```

---

### Task 4: `internal/telegram` — minimal Bot API client

**Files:**
- Create: `internal/telegram/types.go`, `internal/telegram/client.go`
- Test: `internal/telegram/client_test.go`

**Interfaces:**
- Consumes: stdlib only.
- Produces (package `github.com/freaxnx01/bridge/internal/telegram`):
  - Types: `User{ID int64; Username string}`, `Chat{ID int64; Type string}`, `Message{MessageID int; From *User; Chat Chat; Text string}`, `CallbackQuery{ID string; From User; Message *Message; Data string}`, `Update{UpdateID int64; Message *Message; CallbackQuery *CallbackQuery}`, `InlineKeyboardButton{Text, CallbackData string}`, `InlineKeyboardMarkup{InlineKeyboard [][]InlineKeyboardButton}`
  - `var ErrConflict`, `var ErrNotModified` (sentinels matched via `errors.Is`); `type APIError struct{ Method string; Code int; Description string; RetryAfter time.Duration }`
  - `type Option func(*Client)`; `WithBaseURL(u string) Option`, `WithHTTPClient(hc *http.Client) Option`, `WithBackoff(min, max time.Duration) Option`, `WithSleep(fn func(context.Context, time.Duration) error) Option`
  - `func New(token string, opts ...Option) *Client`
  - `func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)`
  - `func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboardMarkup) (Message, error)`
  - `func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, kb *InlineKeyboardMarkup) error`
  - `func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/telegram/client_test.go`:

```go
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456:SECRET-token"

// apiServer serves canned responses in order and records request bodies.
type apiServer struct {
	t         *testing.T
	mu        sync.Mutex
	responses []response
	paths     []string
	bodies    []map[string]any
}

type response struct {
	status int
	body   string
}

func newAPIServer(t *testing.T, rs ...response) (*apiServer, *httptest.Server) {
	a := &apiServer{t: t, responses: rs}
	srv := httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *apiServer) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	a.paths = append(a.paths, r.URL.Path)
	a.bodies = append(a.bodies, body)
	if len(a.responses) == 0 {
		a.t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	res := a.responses[0]
	a.responses = a.responses[1:]
	w.WriteHeader(res.status)
	_, _ = io.WriteString(w, res.body)
}

// recordSleeps returns a sleep func that records delays without waiting.
func recordSleeps(into *[]time.Duration) Option {
	return WithSleep(func(_ context.Context, d time.Duration) error {
		*into = append(*into, d)
		return nil
	})
}

func TestGetUpdates_OffsetTimeoutAndDecode(t *testing.T) {
	a, srv := newAPIServer(t, response{200, `{"ok":true,"result":[
		{"update_id":42,"message":{"message_id":1,"from":{"id":7},"chat":{"id":-100,"type":"group"},"text":"/nav"}},
		{"update_id":43,"callback_query":{"id":"cb1","from":{"id":7},"message":{"message_id":9,"chat":{"id":-100,"type":"group"}},"data":"3:r:1"}}]}`})
	c := New(testToken, WithBaseURL(srv.URL))
	ups, err := c.GetUpdates(context.Background(), 42, 50*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.paths[0] != "/bot"+testToken+"/getUpdates" {
		t.Errorf("path = %s", a.paths[0])
	}
	if a.bodies[0]["offset"] != float64(42) || a.bodies[0]["timeout"] != float64(50) {
		t.Errorf("body = %v", a.bodies[0])
	}
	if len(ups) != 2 || ups[0].Message.Text != "/nav" || ups[0].Message.From.ID != 7 || ups[0].Message.Chat.ID != -100 {
		t.Fatalf("updates = %+v", ups)
	}
	cq := ups[1].CallbackQuery
	if cq == nil || cq.ID != "cb1" || cq.Data != "3:r:1" || cq.Message.MessageID != 9 || cq.From.ID != 7 {
		t.Errorf("callback = %+v", cq)
	}
}

func TestSendMessage_KeyboardAndResult(t *testing.T) {
	a, srv := newAPIServer(t, response{200, `{"ok":true,"result":{"message_id":55,"chat":{"id":1,"type":"private"},"text":"hi"}}`})
	c := New(testToken, WithBaseURL(srv.URL))
	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "A", CallbackData: "1:r:0"}}}}
	m, err := c.SendMessage(context.Background(), 1, "hi", kb)
	if err != nil || m.MessageID != 55 {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
	want := map[string]any{"chat_id": float64(1), "text": "hi", "reply_markup": map[string]any{
		"inline_keyboard": []any{[]any{map[string]any{"text": "A", "callback_data": "1:r:0"}}}}}
	if !reflect.DeepEqual(a.bodies[0], want) {
		t.Errorf("body = %v, want %v", a.bodies[0], want)
	}
}

func TestEditMessageTextAndAnswer(t *testing.T) {
	a, srv := newAPIServer(t, response{200, `{"ok":true,"result":true}`}, response{200, `{"ok":true,"result":true}`})
	c := New(testToken, WithBaseURL(srv.URL))
	if err := c.EditMessageText(context.Background(), 1, 55, "x", &InlineKeyboardMarkup{}); err != nil {
		t.Fatal(err)
	}
	if err := c.AnswerCallbackQuery(context.Background(), "cb1", "⏳ busy"); err != nil {
		t.Fatal(err)
	}
	if a.bodies[0]["message_id"] != float64(55) || a.bodies[1]["callback_query_id"] != "cb1" || a.bodies[1]["text"] != "⏳ busy" {
		t.Errorf("bodies = %v", a.bodies)
	}
}

func TestRetry429_HonoursRetryAfterCapped(t *testing.T) {
	_, srv := newAPIServer(t,
		response{429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 120","parameters":{"retry_after":120}}`},
		response{429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`},
		response{200, `{"ok":true,"result":true}`})
	var sleeps []time.Duration
	c := New(testToken, WithBaseURL(srv.URL), WithBackoff(time.Second, 60*time.Second), recordSleeps(&sleeps))
	if err := c.AnswerCallbackQuery(context.Background(), "x", ""); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{60 * time.Second, 3 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Errorf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestRetry5xx_DoublingBackoffCapped(t *testing.T) {
	_, srv := newAPIServer(t,
		response{502, `bad gateway`}, response{500, `{"ok":false,"error_code":500,"description":"Internal"}`},
		response{503, ``}, response{502, ``}, response{200, `{"ok":true,"result":true}`})
	var sleeps []time.Duration
	c := New(testToken, WithBaseURL(srv.URL), WithBackoff(time.Second, 5*time.Second), recordSleeps(&sleeps))
	if err := c.AnswerCallbackQuery(context.Background(), "x", ""); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Errorf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestConflict_TypedAndNotRetried(t *testing.T) {
	a, srv := newAPIServer(t, response{409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`})
	c := New(testToken, WithBaseURL(srv.URL))
	_, err := c.GetUpdates(context.Background(), 0, time.Second)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if len(a.paths) != 1 {
		t.Errorf("requests = %d, want 1 (409 is not retried)", len(a.paths))
	}
}

func TestNotModified_Recognised(t *testing.T) {
	_, srv := newAPIServer(t, response{400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`})
	err := New(testToken, WithBaseURL(srv.URL)).EditMessageText(context.Background(), 1, 2, "x", nil)
	if !errors.Is(err, ErrNotModified) || errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrNotModified only", err)
	}
}

func TestBadRequest_NotRetried(t *testing.T) {
	a, srv := newAPIServer(t, response{400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`})
	err := New(testToken, WithBaseURL(srv.URL)).AnswerCallbackQuery(context.Background(), "x", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 || len(a.paths) != 1 {
		t.Errorf("err = %v, requests = %d", err, len(a.paths))
	}
}

func TestTransportError_RetriedAndNeverLeaksToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused from now on
	attempts := 0
	stop := errors.New("stop")
	c := New(testToken, WithBaseURL(url), WithSleep(func(context.Context, time.Duration) error {
		attempts++
		if attempts == 2 {
			return stop
		}
		return nil
	}))
	_, err := c.GetUpdates(context.Background(), 0, time.Second)
	if err == nil || !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the aborted retry", err)
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaks the token: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 retries before abort", attempts)
	}
}

func TestContextCancelStopsRetrying(t *testing.T) {
	_, srv := newAPIServer(t, response{502, ``})
	ctx, cancel := context.WithCancel(context.Background())
	c := New(testToken, WithBaseURL(srv.URL), WithSleep(func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}))
	if err := c.AnswerCallbackQuery(ctx, "x", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/telegram/`
Expected: FAIL — `undefined: New`, `undefined: Option`, `undefined: ErrConflict`, …

- [ ] **Step 3: Implement**

Create `internal/telegram/types.go`:

```go
package telegram

// User is a Telegram user (only the fields bridge uses).
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

// Chat is a Telegram chat. Type is "private", "group", "supergroup" or "channel".
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message is an incoming or sent message.
type Message struct {
	MessageID int    `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
}

// CallbackQuery is an inline-keyboard button tap.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update is one getUpdates entry.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// InlineKeyboardButton is a callback button.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// InlineKeyboardMarkup is a message's inline keyboard.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}
```

Create `internal/telegram/client.go`:

```go
// Package telegram is a minimal Telegram Bot API client over net/http:
// long-poll getUpdates, sendMessage, editMessageText and answerCallbackQuery,
// with capped-backoff retries on 429/5xx/network errors. The bot token is part
// of every request URL, so transport errors are stripped of their URL before
// they are returned: the token never reaches an error string or a log line.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

var (
	// ErrConflict matches a 409: another process is polling this token.
	ErrConflict = errors.New("telegram: conflict: another process polls this token")
	// ErrNotModified matches editMessageText's "message is not modified" 400.
	ErrNotModified = errors.New("telegram: message is not modified")
)

// APIError is a non-ok Bot API response.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration // from parameters.retry_after on a 429
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Is maps API errors onto ErrConflict and ErrNotModified for errors.Is.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrConflict:
		return e.Code == http.StatusConflict
	case ErrNotModified:
		return e.Code == http.StatusBadRequest && strings.Contains(e.Description, "message is not modified")
	}
	return false
}

// transportError is a network-level failure (retried).
type transportError struct {
	method string
	err    error
}

func (e *transportError) Error() string { return "telegram " + e.method + ": " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// Client talks to the Bot API. Safe for concurrent use.
type Client struct {
	token      string
	baseURL    string
	hc         *http.Client
	minBackoff time.Duration
	maxBackoff time.Duration
	sleep      func(context.Context, time.Duration) error
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another API root (tests, proxies).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithBackoff sets the first retry delay and the cap (defaults 1s, 60s).
func WithBackoff(min, max time.Duration) Option {
	return func(c *Client) { c.minBackoff, c.maxBackoff = min, max }
}

// WithSleep replaces the retry sleep (tests record delays instead of waiting).
func WithSleep(fn func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = fn }
}

// New returns a client for token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:      token,
		baseURL:    defaultBaseURL,
		hc:         &http.Client{},
		minBackoff: time.Second,
		maxBackoff: 60 * time.Second,
		sleep:      sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// GetUpdates long-polls for updates with update_id >= offset, waiting up to
// timeout server-side. Only messages and callback queries are requested.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	req := struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{offset, int(timeout / time.Second), []string{"message", "callback_query"}}
	var ups []Update
	if err := c.call(ctx, "getUpdates", req, &ups); err != nil {
		return nil, err
	}
	return ups, nil
}

// SendMessage sends plain text (no parse mode) with an optional keyboard.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboardMarkup) (Message, error) {
	req := struct {
		ChatID      int64                 `json:"chat_id"`
		Text        string                `json:"text"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}{chatID, text, kb}
	var m Message
	err := c.call(ctx, "sendMessage", req, &m)
	return m, err
}

// EditMessageText replaces a message's text and keyboard.
func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, kb *InlineKeyboardMarkup) error {
	req := struct {
		ChatID      int64                 `json:"chat_id"`
		MessageID   int                   `json:"message_id"`
		Text        string                `json:"text"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}{chatID, messageID, text, kb}
	return c.call(ctx, "editMessageText", req, nil)
}

// AnswerCallbackQuery stops the tapped button's spinner, optionally with a toast.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	req := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
	}{callbackID, text}
	return c.call(ctx, "answerCallbackQuery", req, nil)
}

// call POSTs req as JSON, retrying 429/5xx/network failures with doubling
// backoff capped at maxBackoff (a 429's retry_after wins, also capped) until
// it succeeds, a non-retryable error occurs, or ctx ends.
func (c *Client) call(ctx context.Context, method string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}
	backoff := c.minBackoff
	for {
		err := c.once(ctx, method, body, out)
		if err == nil {
			return nil
		}
		wait, retry := c.retryDelay(err, backoff)
		if !retry || ctx.Err() != nil {
			return err
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return fmt.Errorf("%w (retry aborted: %w)", err, serr)
		}
		backoff = min(backoff*2, c.maxBackoff)
	}
}

func (c *Client) retryDelay(err error, backoff time.Duration) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == http.StatusTooManyRequests:
			wait := backoff
			if apiErr.RetryAfter > 0 {
				wait = apiErr.RetryAfter
			}
			return min(wait, c.maxBackoff), true
		case apiErr.Code >= 500:
			return backoff, true
		}
		return 0, false
	}
	var te *transportError
	if errors.As(err, &te) {
		return backoff, true
	}
	return 0, false
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) once(ctx context.Context, method string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: build request: %w", method, stripURL(err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("telegram %s: %w", method, ctx.Err())
		}
		return &transportError{method: method, err: stripURL(err)}
	}
	defer func() { _ = resp.Body.Close() }() // read-only body; close error carries no information
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &transportError{method: method, err: stripURL(err)}
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return &APIError{Method: method, Code: resp.StatusCode, Description: http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("telegram %s: decode response (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if !env.OK {
		e := &APIError{Method: method, Code: env.ErrorCode, Description: env.Description}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		if env.Parameters != nil && env.Parameters.RetryAfter > 0 {
			e.RetryAfter = time.Duration(env.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// stripURL drops the request URL (which embeds the token) from a *url.Error.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal/telegram && go vet ./internal/telegram && go test -race ./internal/telegram/`
Expected: `ok  	github.com/freaxnx01/bridge/internal/telegram`

- [ ] **Step 5: Commit**

```bash
git add internal/telegram
git commit -m "feat(telegram): minimal Bot API client with retry and typed errors"
```

---

### Task 5: `internal/bot` — state machine (`Update`)

**Files:**
- Create: `internal/bot/state.go`, `internal/bot/events.go`, `internal/bot/callback.go`, `internal/bot/update.go`
- Test: `internal/bot/update_test.go`

**Interfaces:**
- Consumes (Tasks 1–2): `navdata.RepoEntry`, `navdata.DashRow`, `navdata.Dirty`, `navdata.PickerList`, `navdata.PresentForges`, `navdata.ForgeOpt`, `navdata.Parts`, `navdata.SortKey`, `navdata.RepoLabel`, `navdata.RemoteEntries`, `navdata.RepoChoices`, `navdata.IssueWorktreeName`, `navdata.TruncRunes`; `core.Repo`; `forge.Issue`, `forge.RepoRef`.
- Produces (package `github.com/freaxnx01/bridge/internal/bot`; Task 6 and Task 7 rely on these exact names):
  - consts `PageSize = 8`, `IssuePageSize = 8`, `MaxStarred = 5`, `MaxCallbackBytes = 64`, `MaxFilterRunes = 64`
  - `type Screen int` (`ScreenPicker`, `ScreenDash`, `ScreenIssues`); `type Prompt int` (`PromptNone`, `PromptFilter`, `PromptNewRepoName`, `PromptNewRepoForge`, `PromptNewWorktree`)
  - `type ConfirmKind string` (`ConfirmStop "stop"`, `ConfirmClone "clone"`, `ConfirmIssue "issue"`, `ConfirmNewRepo "repo"`); `type Confirm struct{ Kind ConfirmKind; Index int }`
  - `type RowStatus struct{ Dirty navdata.Dirty; Err bool }`
  - `type State struct { MessageID, Epoch int; Screen Screen; Page int; Filter, Forge string; Local, Remote []navdata.RepoEntry; MRU []string; ReposLoaded bool; Repo core.Repo; Rows []navdata.DashRow; Status map[string]RowStatus; Issues []forge.Issue; IssuesLoaded bool; IssuePage int; Prompt Prompt; NewRepoName string; Confirm *Confirm; Busy, Notice string }`
  - `type PickerItem struct{ Entry navdata.RepoEntry; Star bool }`; `func (s State) PickerItems() []PickerItem`
  - `func ValidName(s string) bool`
  - `type Config struct{ Allow map[int64]bool }`; `func (c Config) Allowed(userID int64) bool`
  - `func Update(cfg Config, s State, ev Event) (State, []Action)`
  - Events (`type Event interface{ isEvent() }`): `Text{UserID, ChatID int64; Text string}`, `Tap{UserID, ChatID int64; MessageID int; Data string}`, `MessageSent{MessageID int}`, `ReposLoaded{Local, Remote []navdata.RepoEntry; MRU []string; Err error}`, `DashLoaded{Repo core.Repo; Rows []navdata.DashRow}`, `StatusLoaded{Repo core.Repo; Status map[string]RowStatus}`, `FetchDone{Repo core.Repo; Err error}`, `IssuesLoaded{Repo core.Repo; Issues []forge.Issue}`, `Started{Slot string; AlreadyRunning, Exited bool; RCURL string; Err error}`, `Stopped{Slot string; Err error}`, `Cloned{Repo core.Repo; Err error}`, `RepoCreated{Repo core.Repo; Err error}`
  - Actions (`type Action interface{ isAction() }`): `NewMessage{}`, `Toast{Text string}`, `Drop{UserID, ChatID int64}`, `SendLink{Text string}`, `LoadRepos{Refresh bool}`, `LoadDash{Repo core.Repo}`, `LoadStatus{Repo core.Repo; Paths []string}`, `Fetch{Repo core.Repo}`, `LoadIssues{Repo core.Repo}`, `Start{Repo core.Repo; Worktree, Label string; Create bool}`, `Stop{Slot string}`, `Clone{Ref forge.RepoRef}`, `CreateRepo{Name, Forge string; Private bool}`
  - unexported, used by Task 6: `data(epoch int, verb string, args ...string) string` (callback data `"<epoch>:<verb>[:<arg>…]"`), `pageCount(n, size int) int`, `clamp(v, lo, hi int) int`, `hasForgeIDs(r core.Repo) bool`, `rowName(r navdata.DashRow) string`, const `outdatedText = "⚠ outdated — /nav"`

Callback verbs (all prefixed `<epoch>:`): picker `r:<i>` open item, `pg:±1` page, `fg` forge cycle, `f` filter prompt, `fx` clear filter, `nr` new repo, `nc:<i>` repo choice, `rf` refresh; dashboard `w:<i>` start/open row, `x:<i>` stop row, `nw` new worktree, `is` issues, `rf` reload, `bk` back to repos; issues `i:<i>` issue, `ip:±1` page, `bk` back to dashboard; overlays `ok:<kind>:<i>`, `no`; `nop` (page label). A tap is outdated unless its epoch equals `State.Epoch` **and** its message id equals `State.MessageID`; the epoch changes whenever a button index may change meaning (lists replaced, screen/prompt/confirm entered or left).

- [ ] **Step 1: Write the failing tests**

Create `internal/bot/update_test.go`:

```go
package bot

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

const (
	me    int64 = 111
	other int64 = 222
	chat  int64 = -1001 // a group chat
)

var cfg = Config{Allow: map[int64]bool{me: true}}

func local(forgeName, owner, name string) navdata.RepoEntry {
	r := core.Repo{Name: name, Forge: forgeName, Owner: owner, Visibility: "public", Path: "/r/" + forgeName + "/" + name}
	return navdata.RepoEntry{Label: navdata.RepoLabel(r), Repo: r}
}

func remote(forgeName, owner, name string) navdata.RepoEntry {
	return navdata.RemoteEntries([]forge.RepoRef{{Forge: forgeName, Owner: owner, Name: name, Visibility: "private"}})[0]
}

// picker returns a loaded picker state with message 10 on screen.
func picker(local, remote []navdata.RepoEntry) State {
	s, _ := Update(cfg, State{}, Text{UserID: me, ChatID: chat, Text: "/nav"})
	s, _ = Update(cfg, s, MessageSent{MessageID: 10})
	s, _ = Update(cfg, s, ReposLoaded{Local: local, Remote: remote})
	return s
}

// tap taps a button of the current keyboard as user me.
func tap(t *testing.T, s State, verb string, args ...string) (State, []Action) {
	t.Helper()
	return Update(cfg, s, Tap{UserID: me, ChatID: chat, MessageID: s.MessageID, Data: data(s.Epoch, verb, args...)})
}

func text(s State, msg string) (State, []Action) {
	return Update(cfg, s, Text{UserID: me, ChatID: chat, Text: msg})
}

func only[T Action](t *testing.T, acts []Action) T {
	t.Helper()
	if len(acts) != 1 {
		t.Fatalf("actions = %#v, want exactly one %T", acts, *new(T))
	}
	a, ok := acts[0].(T)
	if !ok {
		t.Fatalf("action = %#v, want %T", acts[0], *new(T))
	}
	return a
}

func dash(t *testing.T, rows ...navdata.DashRow) State {
	t.Helper()
	s := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge")}, nil)
	s, _ = tap(t, s, "r", "0")
	s, _ = Update(cfg, s, DashLoaded{Repo: s.Repo, Rows: rows})
	return s
}

var (
	baseRow  = navdata.DashRow{IsBase: true, Branch: "main", Path: "/r/github/bridge"}
	liveRow  = navdata.DashRow{Worktree: "reorg", Path: "/r/github/bridge/.worktrees/reorg", HasSession: true, SlotID: "bridge-wt-reorg", State: "detached"}
	plainRow = navdata.DashRow{Worktree: "tg", Path: "/r/github/bridge/.worktrees/tg"}
)

func TestNav_OpensFreshPickerInNewMessage(t *testing.T) {
	s, acts := Update(cfg, State{Epoch: 4, Screen: ScreenDash, Filter: "x"}, Text{UserID: me, ChatID: chat, Text: "/nav@freax_bridge_bot"})
	if !reflect.DeepEqual(acts, []Action{NewMessage{}, LoadRepos{}}) {
		t.Errorf("actions = %#v", acts)
	}
	if s.Screen != ScreenPicker || s.Filter != "" || s.Epoch != 5 || s.MessageID != 0 {
		t.Errorf("state = %+v", s)
	}
}

func TestNotAllowlisted_TextDropped(t *testing.T) {
	s0 := State{Epoch: 2}
	s, acts := Update(cfg, s0, Text{UserID: other, ChatID: chat, Text: "/nav"})
	if !reflect.DeepEqual(acts, []Action{Drop{UserID: other, ChatID: chat}}) || !reflect.DeepEqual(s, s0) {
		t.Errorf("state = %+v, actions = %#v", s, acts)
	}
}

// Review Focus: in a group, another member taps a valid button on the
// allowlisted user's message. Nothing may happen.
func TestNotAllowlisted_TapInGroupOnValidButtonDropped(t *testing.T) {
	s0 := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge")}, nil)
	s, acts := Update(cfg, s0, Tap{UserID: other, ChatID: chat, MessageID: s0.MessageID, Data: data(s0.Epoch, "r", "0")})
	if !reflect.DeepEqual(acts, []Action{Drop{UserID: other, ChatID: chat}}) {
		t.Errorf("actions = %#v", acts)
	}
	if !reflect.DeepEqual(s, s0) {
		t.Errorf("state changed: %+v", s)
	}
}

func TestOutdatedTaps(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "a")}, nil)
	cases := map[string]Tap{
		"old epoch":     {UserID: me, MessageID: s.MessageID, Data: data(s.Epoch-1, "r", "0")},
		"old message":   {UserID: me, MessageID: s.MessageID - 1, Data: data(s.Epoch, "r", "0")},
		"index gone":    {UserID: me, MessageID: s.MessageID, Data: data(s.Epoch, "r", "7")},
		"garbage":       {UserID: me, MessageID: s.MessageID, Data: "zzz"},
		"over 64 bytes": {UserID: me, MessageID: s.MessageID, Data: data(s.Epoch, "r", strings.Repeat("9", 70))},
	}
	for name, tp := range cases {
		got, acts := Update(cfg, s, tp)
		if !reflect.DeepEqual(acts, []Action{Toast{Text: outdatedText}}) || !reflect.DeepEqual(got, s) {
			t.Errorf("%s: state changed or actions = %#v", name, acts)
		}
	}
	// After a restart the chat has no state at all.
	if _, acts := Update(cfg, State{}, Tap{UserID: me, MessageID: 10, Data: "3:r:0"}); !reflect.DeepEqual(acts, outdated()) {
		t.Errorf("restart: actions = %#v", acts)
	}
}

func TestPickerPaging(t *testing.T) {
	var many []navdata.RepoEntry
	for i := 0; i < 20; i++ {
		many = append(many, local("github", "o", fmt.Sprintf("repo%02d", i)))
	}
	s := picker(many, nil)
	s, _ = tap(t, s, "pg", "+1")
	s, _ = tap(t, s, "pg", "+1")
	if s.Page != 2 {
		t.Fatalf("page = %d, want 2", s.Page)
	}
	s, _ = tap(t, s, "pg", "+1")
	if s.Page != 2 {
		t.Errorf("page = %d, want clamped at 2", s.Page)
	}
	s, _ = tap(t, s, "pg", "-1")
	if s.Page != 1 {
		t.Errorf("page = %d, want 1", s.Page)
	}
}

func TestFilterPromptAndClear(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "bridge"), local("github", "o", "notes")}, nil)
	s.Page = 1
	s, _ = tap(t, s, "f")
	if s.Prompt != PromptFilter {
		t.Fatalf("prompt = %v", s.Prompt)
	}
	s, acts := text(s, "  BRID ")
	if len(acts) != 0 || s.Filter != "BRID" || s.Prompt != PromptNone || s.Page != 0 {
		t.Fatalf("state = %+v, actions = %#v", s, acts)
	}
	if items := s.PickerItems(); len(items) != 1 || items[0].Entry.Repo.Name != "bridge" {
		t.Errorf("items = %+v", items)
	}
	s, _ = tap(t, s, "fx")
	if s.Filter != "" || len(s.PickerItems()) != 2 {
		t.Errorf("filter not cleared: %+v", s)
	}
}

func TestForgeFilterCycles(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "a"), local("forgejo", "o", "b")}, nil)
	var seen []string
	for i := 0; i < 3; i++ {
		s, _ = tap(t, s, "fg")
		seen = append(seen, s.Forge)
	}
	if !reflect.DeepEqual(seen, []string{"github", "forgejo", ""}) {
		t.Errorf("cycle = %v", seen)
	}
}

func TestMRUFirstThenByName(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "zeta"), local("github", "o", "alpha"), local("forgejo", "o", "mid")},
		[]navdata.RepoEntry{remote("github", "o", "beta")})
	s.MRU = []string{"/r/github/zeta"}
	var got []string
	for _, it := range s.PickerItems() {
		_, _, _, n := navdata.Parts(it.Entry)
		got = append(got, fmt.Sprintf("%s:%v", n, it.Star))
	}
	if want := []string{"zeta:true", "alpha:false", "beta:false", "mid:false"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Review Focus: the same repo name on two forges must open the repo whose
// button was tapped, never the other one.
func TestSameNameOnTwoForges_OpensTheTappedOne(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge"), local("forgejo", "freaxnx01", "bridge")}, nil)
	items := s.PickerItems()
	for i, it := range items {
		_, acts := tap(t, s, "r", fmt.Sprint(i))
		ld := only[LoadDash](t, acts)
		if ld.Repo.Path != it.Entry.Repo.Path {
			t.Errorf("item %d: opened %s, want %s", i, ld.Repo.Path, it.Entry.Repo.Path)
		}
	}
	if items[0].Entry.Repo.Path == items[1].Entry.Repo.Path {
		t.Fatal("both items point at the same repo")
	}
}

func TestOpenDashboard_LoadsRowsStatusFetchIssues(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge")}, nil)
	s, acts := tap(t, s, "r", "0")
	if s.Screen != ScreenDash || only[LoadDash](t, acts).Repo.Name != "bridge" {
		t.Fatalf("state = %+v, actions = %#v", s, acts)
	}
	s, acts = Update(cfg, s, DashLoaded{Repo: s.Repo, Rows: []navdata.DashRow{baseRow, liveRow}})
	want := []Action{
		LoadStatus{Repo: s.Repo, Paths: []string{baseRow.Path, liveRow.Path}},
		Fetch{Repo: s.Repo},
		LoadIssues{Repo: s.Repo},
	}
	if !reflect.DeepEqual(acts, want) {
		t.Errorf("actions = %#v, want %#v", acts, want)
	}
	if _, acts := Update(cfg, s, FetchDone{Repo: s.Repo}); !reflect.DeepEqual(acts, want[:1]) {
		t.Errorf("after fetch: %#v", acts)
	}
	if _, acts := Update(cfg, s, FetchDone{Repo: s.Repo, Err: errors.New("offline")}); len(acts) != 0 {
		t.Errorf("failed fetch: %#v", acts)
	}
	stale := core.Repo{Name: "other", Path: "/elsewhere"}
	if got, acts := Update(cfg, s, DashLoaded{Repo: stale, Rows: nil}); len(acts) != 0 || got.Rows == nil {
		t.Errorf("stale result applied: %+v %#v", got, acts)
	}
}

func TestStart_RowAndResults(t *testing.T) {
	s := dash(t, baseRow, liveRow)
	s, acts := tap(t, s, "w", "0")
	if st := only[Start](t, acts); st.Worktree != "" || st.Repo.Name != "bridge" || st.Create {
		t.Errorf("start = %+v", st)
	}
	if s.Busy == "" {
		t.Fatal("busy not set")
	}
	cases := []struct {
		ev       Started
		notice   string
		wantLink bool
	}{
		{Started{Slot: "bridge", RCURL: "https://claude.ai/code/session_A"}, "launched bridge", true},
		{Started{Slot: "bridge", AlreadyRunning: true, RCURL: "https://claude.ai/code/session_A"}, "already running: bridge", true},
		{Started{Slot: "bridge"}, "session bridge running, no link yet", false},
		{Started{Slot: "bridge", Exited: true}, "⚠ session bridge exited during startup", false},
		{Started{Err: errors.New("bridge: tmux: boom\nmore")}, "⚠ tmux: boom", false},
	}
	for _, c := range cases {
		got, acts := Update(cfg, s, c.ev)
		if got.Busy != "" || got.Notice != c.notice {
			t.Errorf("%+v: busy=%q notice=%q, want notice %q", c.ev, got.Busy, got.Notice, c.notice)
		}
		hasLink := false
		for _, a := range acts {
			if l, ok := a.(SendLink); ok {
				hasLink = strings.HasSuffix(l.Text, c.ev.RCURL)
			}
		}
		if hasLink != c.wantLink {
			t.Errorf("%+v: link sent = %v, want %v (%#v)", c.ev, hasLink, c.wantLink, acts)
		}
		if _, ok := acts[len(acts)-1].(LoadDash); !ok {
			t.Errorf("%+v: dashboard not reloaded: %#v", c.ev, acts)
		}
	}
}

// Review Focus: a flood of taps while an action runs must not start anything
// else — every tap only gets a toast.
func TestFloodOfTapsWhileBusy(t *testing.T) {
	s := dash(t, baseRow, liveRow, plainRow)
	s, _ = tap(t, s, "w", "0")
	busy := s
	for _, verb := range [][]string{{"w", "1"}, {"w", "0"}, {"x", "1"}, {"nw"}, {"bk"}, {"rf"}} {
		got, acts := tap(t, busy, verb[0], verb[1:]...)
		toast := only[Toast](t, acts)
		if !strings.HasPrefix(toast.Text, "⏳ still working") {
			t.Errorf("%v: toast = %q", verb, toast.Text)
		}
		if !reflect.DeepEqual(got, busy) {
			t.Errorf("%v: state changed while busy", verb)
		}
	}
	if _, acts := text(busy, "/nav"); !reflect.DeepEqual(acts, []Action{NewMessage{}, LoadRepos{}}) {
		t.Errorf("/nav while busy: %#v", acts)
	}
}

func TestStopConfirmAndCancel(t *testing.T) {
	s := dash(t, baseRow, liveRow)
	if _, acts := tap(t, s, "x", "0"); !reflect.DeepEqual(acts, outdated()) {
		t.Errorf("stop on a row without session: %#v", acts)
	}
	s, acts := tap(t, s, "x", "1")
	if len(acts) != 0 || s.Confirm == nil || *s.Confirm != (Confirm{ConfirmStop, 1}) {
		t.Fatalf("confirm = %+v, actions = %#v", s.Confirm, acts)
	}
	if _, acts := tap(t, s, "ok", "stop", "0"); !reflect.DeepEqual(acts, outdated()) {
		t.Errorf("mismatched confirm: %#v", acts)
	}
	cancelled, _ := tap(t, s, "no")
	if cancelled.Confirm != nil || cancelled.Busy != "" {
		t.Errorf("cancel: %+v", cancelled)
	}
	s, acts = tap(t, s, "ok", "stop", "1")
	if only[Stop](t, acts).Slot != "bridge-wt-reorg" || s.Busy == "" {
		t.Errorf("stop: %+v %#v", s, acts)
	}
	s, acts = Update(cfg, s, Stopped{Slot: "bridge-wt-reorg"})
	if s.Notice != "stopped bridge-wt-reorg" || only[LoadDash](t, acts).Repo.Name != "bridge" {
		t.Errorf("stopped: %+v %#v", s, acts)
	}
}

func TestNewWorktreeNameValidation(t *testing.T) {
	s := dash(t, baseRow)
	s, _ = tap(t, s, "nw")
	for _, bad := range []string{"../x", "a b", "..", ".", "x/y", "ü", strings.Repeat("a", 41), "$(rm)"} {
		got, acts := text(s, bad)
		if len(acts) != 0 || got.Notice != invalidNameText || got.Prompt != PromptNewWorktree {
			t.Errorf("%q accepted: %+v %#v", bad, got, acts)
		}
	}
	s, acts := text(s, "feat-1.2_x")
	if st := only[Start](t, acts); st.Worktree != "feat-1.2_x" || !st.Create || st.Label != "" {
		t.Errorf("start = %+v", st)
	}
	if s.Prompt != PromptNone || s.Busy == "" {
		t.Errorf("state = %+v", s)
	}
}

// Review Focus: an issue title with emoji and a slash yields a safe worktree
// name and a valid label.
func TestIssueLaunch_EmojiAndSlashTitle(t *testing.T) {
	s := dash(t, baseRow)
	s, _ = Update(cfg, s, IssuesLoaded{Repo: s.Repo, Issues: []forge.Issue{{Number: 7, Title: "🚀 fix/launch: crash"}}})
	s, _ = tap(t, s, "is")
	if s.Screen != ScreenIssues {
		t.Fatalf("screen = %v", s.Screen)
	}
	s, _ = tap(t, s, "i", "0")
	s, acts := tap(t, s, "ok", "issue", "0")
	want := Start{Repo: s.Repo, Worktree: "7-fix-launch-crash", Label: "#7 [🚀 fix/launch: crash]", Create: true}
	if got := only[Start](t, acts); !reflect.DeepEqual(got, want) {
		t.Errorf("start = %+v, want %+v", got, want)
	}
	s, _ = Update(cfg, s, Started{Slot: "bridge-wt-7-fix-launch-crash", RCURL: "https://claude.ai/code/session_B"})
	if s.Screen != ScreenDash {
		t.Errorf("screen after issue start = %v, want dashboard", s.Screen)
	}
}

func TestCloneRemoteRepo(t *testing.T) {
	s := picker(nil, []navdata.RepoEntry{remote("github", "freaxnx01", "obsidian-it")})
	s, acts := tap(t, s, "r", "0")
	if len(acts) != 0 || s.Confirm == nil || s.Confirm.Kind != ConfirmClone {
		t.Fatalf("confirm = %+v %#v", s.Confirm, acts)
	}
	s, acts = tap(t, s, "ok", "clone", "0")
	if only[Clone](t, acts).Ref.Name != "obsidian-it" || s.Busy == "" {
		t.Fatalf("clone: %+v %#v", s, acts)
	}
	repo := core.Repo{Name: "obsidian-it", Path: "/r/github/obsidian-it", Forge: "github", Owner: "freaxnx01"}
	s, acts = Update(cfg, s, Cloned{Repo: repo})
	if s.Screen != ScreenDash || s.Repo.Path != repo.Path || s.Busy != "" {
		t.Errorf("after clone: %+v", s)
	}
	if !reflect.DeepEqual(acts, []Action{LoadDash{Repo: repo}, LoadRepos{}}) {
		t.Errorf("actions = %#v", acts)
	}
	failed, acts := Update(cfg, State{Busy: "⏳ cloning x…"}, Cloned{Err: errors.New("clone: exit status 128")})
	if failed.Busy != "" || failed.Notice != "⚠ clone: exit status 128" || len(acts) != 0 {
		t.Errorf("failed clone: %+v %#v", failed, acts)
	}
}

func TestNewRepoFlow(t *testing.T) {
	s := picker(nil, nil)
	s, _ = tap(t, s, "nr")
	s, acts := text(s, "bad name")
	if len(acts) != 0 || s.Notice != invalidNameText {
		t.Fatalf("invalid name: %+v", s)
	}
	s, _ = text(s, "demo")
	if s.Prompt != PromptNewRepoForge || s.NewRepoName != "demo" {
		t.Fatalf("state = %+v", s)
	}
	s, _ = tap(t, s, "nc", "2")
	s, acts = tap(t, s, "ok", "repo", "2")
	if got := only[CreateRepo](t, acts); got != (CreateRepo{Name: "demo", Forge: "github", Private: true}) {
		t.Errorf("create = %+v", got)
	}
	repo := core.Repo{Name: "demo", Path: "/r/github/demo"}
	s, acts = Update(cfg, s, RepoCreated{Repo: repo})
	if s.Screen != ScreenDash || s.Prompt != PromptNone || s.NewRepoName != "" || !reflect.DeepEqual(acts, []Action{LoadDash{Repo: repo}, LoadRepos{}}) {
		t.Errorf("after create: %+v %#v", s, acts)
	}
}

func TestRefreshRepos(t *testing.T) {
	s := picker(nil, nil)
	s, acts := tap(t, s, "rf")
	if !reflect.DeepEqual(acts, []Action{LoadRepos{Refresh: true}}) || s.Busy != busyRefreshRepos {
		t.Fatalf("refresh: %+v %#v", s, acts)
	}
	s, _ = Update(cfg, s, ReposLoaded{Err: errors.New("github: 401")})
	if s.Busy != "" || s.Notice != "⚠ github: 401" {
		t.Errorf("after refresh: %+v", s)
	}
}

func TestBackToReposKeepsPageAndFilter(t *testing.T) {
	var many []navdata.RepoEntry
	for i := 0; i < 20; i++ {
		many = append(many, local("github", "o", fmt.Sprintf("bridge%02d", i)))
	}
	s := picker(many, nil)
	s, _ = tap(t, s, "f")
	s, _ = text(s, "bridge")
	s, _ = tap(t, s, "pg", "+1")
	s, _ = tap(t, s, "r", "9")
	s, acts := tap(t, s, "bk")
	if s.Screen != ScreenPicker || s.Page != 1 || s.Filter != "bridge" || !reflect.DeepEqual(acts, []Action{LoadRepos{}}) {
		t.Errorf("back: page=%d filter=%q acts=%#v", s.Page, s.Filter, acts)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "A-b_c.d", strings.Repeat("x", 40), ".env"} {
		if !ValidName(ok) {
			t.Errorf("ValidName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "...", "a/b", "a b", "ü", strings.Repeat("x", 41)} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true", bad)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bot/`
Expected: FAIL — `undefined: Config`, `undefined: Update`, `undefined: State`, …

- [ ] **Step 3: Implement**

Create `internal/bot/state.go`:

```go
// Package bot is the `bridge bot` screen state machine: per-chat State,
// Update(cfg, state, event) → (state, actions) and Render(state) → (text,
// keyboard). It is pure: every bit of I/O is an Action the runner executes,
// whose result comes back as an Event.
package bot

import (
	"regexp"
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

const (
	// PageSize is the number of repos per picker page (2 per row).
	PageSize = 8
	// IssuePageSize is the number of issues per issues page.
	IssuePageSize = 8
	// MaxStarred caps the ⭐ MRU block at the top of the picker (nav's Recent cap).
	MaxStarred = 5
	// MaxCallbackBytes is Telegram's callback_data limit.
	MaxCallbackBytes = 64
	// MaxFilterRunes bounds a filter typed in chat.
	MaxFilterRunes = 64
)

// Screen is the screen a chat is on.
type Screen int

const (
	ScreenPicker Screen = iota
	ScreenDash
	ScreenIssues
)

// Prompt is what the next free-text message means.
type Prompt int

const (
	PromptNone         Prompt = iota
	PromptFilter              // next text is the picker filter
	PromptNewRepoName         // next text is the new repo's name
	PromptNewRepoForge        // waiting for a forge × visibility button
	PromptNewWorktree         // next text is the new worktree's name
)

// ConfirmKind names what a ✅ button confirms.
type ConfirmKind string

const (
	ConfirmStop    ConfirmKind = "stop"
	ConfirmClone   ConfirmKind = "clone"
	ConfirmIssue   ConfirmKind = "issue"
	ConfirmNewRepo ConfirmKind = "repo"
)

// Confirm is a pending yes/no question about the item at Index (picker item,
// dashboard row, issue or repo choice, by Kind).
type Confirm struct {
	Kind  ConfirmKind
	Index int
}

// RowStatus is a dashboard row's git status; Err marks a failed read.
type RowStatus struct {
	Dirty navdata.Dirty
	Err   bool
}

// State is one chat's state. Treat it as a value: Update never mutates the
// slices or maps it receives, it replaces them.
type State struct {
	MessageID int // the chat's single edited message; 0 => none yet
	// Epoch is stamped into every button. It changes whenever the meaning of
	// a button index may change, so taps on an older keyboard are "outdated".
	Epoch int

	Screen      Screen
	Page        int
	Filter      string
	Forge       string // "" = All
	Local       []navdata.RepoEntry
	Remote      []navdata.RepoEntry
	MRU         []string
	ReposLoaded bool

	Repo         core.Repo
	Rows         []navdata.DashRow // nil => loading
	Status       map[string]RowStatus
	Issues       []forge.Issue
	IssuesLoaded bool
	IssuePage    int

	Prompt      Prompt
	NewRepoName string
	Confirm     *Confirm
	Busy        string // non-empty while a slow action runs; taps are refused
	Notice      string // one-line result or "⚠ …" error
}

// PickerItem is one picker button: an entry and whether it is in the ⭐ block.
type PickerItem struct {
	Entry navdata.RepoEntry
	Star  bool
}

// PickerItems is the full picker list (all pages): up to MaxStarred MRU repos
// first (⭐, most recent first), then everything else alphabetically by repo
// name. Button indexes ("r:<i>") index into this slice.
func (s State) PickerItems() []PickerItem {
	list := navdata.PickerList(s.Local, s.Remote, s.Forge, s.Filter)
	rank := make(map[string]int, len(s.MRU))
	for i, p := range s.MRU {
		if _, seen := rank[p]; !seen {
			rank[p] = i
		}
	}
	var starred, rest []PickerItem
	for _, e := range list {
		if e.Remote == nil {
			if _, ok := rank[e.Repo.Path]; ok {
				starred = append(starred, PickerItem{Entry: e, Star: true})
				continue
			}
		}
		rest = append(rest, PickerItem{Entry: e})
	}
	sort.SliceStable(starred, func(i, j int) bool {
		return rank[starred[i].Entry.Repo.Path] < rank[starred[j].Entry.Repo.Path]
	})
	if len(starred) > MaxStarred {
		for _, it := range starred[MaxStarred:] {
			it.Star = false
			rest = append(rest, it)
		}
		starred = starred[:MaxStarred]
	}
	sort.SliceStable(rest, func(i, j int) bool { return itemSortKey(rest[i]) < itemSortKey(rest[j]) })
	return append(starred, rest...)
}

func itemSortKey(it PickerItem) string {
	_, _, _, name := navdata.Parts(it.Entry)
	return strings.ToLower(name) + "\x00" + navdata.SortKey(it.Entry.Label)
}

// nameRE is the allowed shape of a repo/worktree name typed in chat.
// Compiled once; *regexp.Regexp is immutable and safe for concurrent use.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)

// ValidName reports whether s may be used as a new repo or worktree name:
// ^[A-Za-z0-9._-]{1,40}$ and not made only of dots ("." / ".." would resolve
// to the repo itself or its parent).
func ValidName(s string) bool {
	return nameRE.MatchString(s) && strings.Trim(s, ".") != ""
}

func pageCount(n, size int) int {
	if n <= 0 {
		return 1
	}
	return (n + size - 1) / size
}

func clamp(v, lo, hi int) int {
	if hi < lo || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func hasForgeIDs(r core.Repo) bool { return r.Forge != "" && r.Owner != "" && r.Name != "" }
```

Create `internal/bot/events.go`:

```go
package bot

import (
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

// Event is an input to Update: a Telegram message or tap, or the result of an
// Action the runner executed.
type Event interface{ isEvent() }

// Text is a text message (a /command, a filter or a name).
type Text struct {
	UserID, ChatID int64
	Text           string
}

// Tap is an inline-button tap on message MessageID.
type Tap struct {
	UserID, ChatID int64
	MessageID      int
	Data           string
}

// MessageSent reports the id of the message the runner sent for NewMessage.
type MessageSent struct{ MessageID int }

// ReposLoaded is LoadRepos' result. Err is shown, the lists are still used.
type ReposLoaded struct {
	Local, Remote []navdata.RepoEntry
	MRU           []string
	Err           error
}

// DashLoaded is LoadDash's result.
type DashLoaded struct {
	Repo core.Repo
	Rows []navdata.DashRow
}

// StatusLoaded is LoadStatus' result, keyed by worktree path.
type StatusLoaded struct {
	Repo   core.Repo
	Status map[string]RowStatus
}

// FetchDone is Fetch's result.
type FetchDone struct {
	Repo core.Repo
	Err  error
}

// IssuesLoaded is LoadIssues' result.
type IssuesLoaded struct {
	Repo   core.Repo
	Issues []forge.Issue
}

// Started is Start's result.
type Started struct {
	Slot           string
	AlreadyRunning bool
	Exited         bool // the session died before printing a link
	RCURL          string
	Err            error
}

// Stopped is Stop's result.
type Stopped struct {
	Slot string
	Err  error
}

// Cloned is Clone's result.
type Cloned struct {
	Repo core.Repo
	Err  error
}

// RepoCreated is CreateRepo's result.
type RepoCreated struct {
	Repo core.Repo
	Err  error
}

func (Text) isEvent()         {}
func (Tap) isEvent()          {}
func (MessageSent) isEvent()  {}
func (ReposLoaded) isEvent()  {}
func (DashLoaded) isEvent()   {}
func (StatusLoaded) isEvent() {}
func (FetchDone) isEvent()    {}
func (IssuesLoaded) isEvent() {}
func (Started) isEvent()      {}
func (Stopped) isEvent()      {}
func (Cloned) isEvent()       {}
func (RepoCreated) isEvent()  {}

// Action is an output of Update for the runner to execute.
type Action interface{ isAction() }

// NewMessage: send Render(state) as a new message (then feed MessageSent).
type NewMessage struct{}

// Toast: the text to answer the tap's callback query with.
type Toast struct{ Text string }

// Drop: ignore the update (sender not allowlisted); log one line.
type Drop struct{ UserID, ChatID int64 }

// SendLink: send Text as a separate new message (an RC link → phone notification).
type SendLink struct{ Text string }

// LoadRepos: reload local repos, the remote cache and the MRU; Refresh also
// re-queries the forges (slow).
type LoadRepos struct{ Refresh bool }

// LoadDash: load Repo's dashboard rows.
type LoadDash struct{ Repo core.Repo }

// LoadStatus: read git status for each path.
type LoadStatus struct {
	Repo  core.Repo
	Paths []string
}

// Fetch: git fetch Repo (fresh ahead/behind).
type Fetch struct{ Repo core.Repo }

// LoadIssues: load Repo's open issues (cached).
type LoadIssues struct{ Repo core.Repo }

// Start: start or open a session (Create: make the worktree first). Label
// overrides the session name ("#123 [title]").
type Start struct {
	Repo     core.Repo
	Worktree string
	Label    string
	Create   bool
}

// Stop: kill Slot's tmux session.
type Stop struct{ Slot string }

// Clone: clone a remote-only repo.
type Clone struct{ Ref forge.RepoRef }

// CreateRepo: create, then clone, a new repo.
type CreateRepo struct {
	Name    string
	Forge   string
	Private bool
}

func (NewMessage) isAction() {}
func (Toast) isAction()      {}
func (Drop) isAction()       {}
func (SendLink) isAction()   {}
func (LoadRepos) isAction()  {}
func (LoadDash) isAction()   {}
func (LoadStatus) isAction() {}
func (Fetch) isAction()      {}
func (LoadIssues) isAction() {}
func (Start) isAction()      {}
func (Stop) isAction()       {}
func (Clone) isAction()      {}
func (CreateRepo) isAction() {}
```

Create `internal/bot/callback.go`:

```go
package bot

import (
	"strconv"
	"strings"
)

// data builds callback data "<epoch>:<verb>[:<arg>…]". Buttons only ever
// carry short ids resolved against the chat's State — never paths or names.
func data(epoch int, verb string, args ...string) string {
	return strings.Join(append([]string{strconv.Itoa(epoch), verb}, args...), ":")
}

// tapData is parsed callback data.
type tapData struct {
	epoch int
	verb  string
	args  []string
}

func parseData(d string) (tapData, bool) {
	if d == "" || len(d) > MaxCallbackBytes {
		return tapData{}, false
	}
	parts := strings.Split(d, ":")
	if len(parts) < 2 {
		return tapData{}, false
	}
	epoch, err := strconv.Atoi(parts[0])
	if err != nil {
		return tapData{}, false
	}
	return tapData{epoch: epoch, verb: parts[1], args: parts[2:]}, true
}

// intArg parses args[i] ("+1" and "-1" included).
func (t tapData) intArg(i int) (int, bool) {
	if i >= len(t.args) {
		return 0, false
	}
	n, err := strconv.Atoi(t.args[i])
	return n, err == nil
}
```

Create `internal/bot/update.go`:

```go
package bot

import (
	"fmt"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/navdata"
)

const (
	outdatedText     = "⚠ outdated — /nav"
	invalidNameText  = "⚠ invalid name — use A-Z a-z 0-9 . _ - (max 40)"
	busyRefreshRepos = "⏳ refreshing repos…"
	noticeRefreshing = "⏳ refreshing…"
)

// Config is the bot's static configuration.
type Config struct {
	// Allow is the set of Telegram user ids the bot answers. Everyone else —
	// including other members of a group the bot is in — is dropped.
	Allow map[int64]bool
}

// Allowed reports whether userID is allowlisted.
func (c Config) Allowed(userID int64) bool { return c.Allow[userID] }

// Update applies one event to a chat's state and returns the new state plus
// the actions to execute. It is pure.
func Update(cfg Config, s State, ev Event) (State, []Action) {
	switch e := ev.(type) {
	case Text:
		if !cfg.Allowed(e.UserID) {
			return s, []Action{Drop{UserID: e.UserID, ChatID: e.ChatID}}
		}
		return onText(s, e)
	case Tap:
		if !cfg.Allowed(e.UserID) {
			return s, []Action{Drop{UserID: e.UserID, ChatID: e.ChatID}}
		}
		return onTap(s, e)
	case MessageSent:
		s.MessageID = e.MessageID
		return s, nil
	case ReposLoaded:
		return onReposLoaded(s, e), nil
	case DashLoaded:
		return onDashLoaded(s, e)
	case StatusLoaded:
		return onStatusLoaded(s, e), nil
	case FetchDone:
		return onFetchDone(s, e)
	case IssuesLoaded:
		return onIssuesLoaded(s, e), nil
	case Started:
		return onStarted(s, e)
	case Stopped:
		return onStopped(s, e)
	case Cloned:
		return onOpened(s, e.Repo, e.Err)
	case RepoCreated:
		s.Prompt, s.NewRepoName = PromptNone, ""
		return onOpened(s, e.Repo, e.Err)
	}
	return s, nil
}

func outdated() []Action { return []Action{Toast{Text: outdatedText}} }

// command returns the lower-cased /command of text ("/nav@bridge_bot x" →
// "/nav"), or "" when text is not a command.
func command(text string) string {
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	cmd, _, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(cmd, "@")
	return strings.ToLower(cmd)
}

func onText(s State, e Text) (State, []Action) {
	txt := strings.TrimSpace(e.Text)
	switch command(txt) {
	case "":
	case "/nav", "/start":
		// A fresh picker in a new message; repo lists and a running action survive.
		return State{
			Epoch: s.Epoch + 1, Busy: s.Busy,
			Local: s.Local, Remote: s.Remote, MRU: s.MRU, ReposLoaded: s.ReposLoaded,
		}, []Action{NewMessage{}, LoadRepos{}}
	default:
		s.Notice = "⚠ unknown command — /nav"
		return s, nil
	}
	switch s.Prompt {
	case PromptFilter:
		s.Filter = navdata.TruncRunes(txt, MaxFilterRunes)
		s.Prompt, s.Page, s.Notice = PromptNone, 0, ""
		s.Epoch++
		return s, nil
	case PromptNewRepoName:
		if !ValidName(txt) {
			s.Notice = invalidNameText
			return s, nil
		}
		s.NewRepoName, s.Prompt, s.Notice = txt, PromptNewRepoForge, ""
		s.Epoch++
		return s, nil
	case PromptNewWorktree:
		if !ValidName(txt) {
			s.Notice = invalidNameText
			return s, nil
		}
		if s.Busy != "" {
			s.Notice = "⏳ still working — try again when done"
			return s, nil
		}
		s.Prompt, s.Notice = PromptNone, ""
		s.Busy = "⏳ creating worktree " + txt + "…"
		s.Epoch++
		return s, []Action{Start{Repo: s.Repo, Worktree: txt, Create: true}}
	}
	s.Notice = "send /nav to open the picker"
	return s, nil
}

func onTap(s State, e Tap) (State, []Action) {
	d, ok := parseData(e.Data)
	if !ok || s.MessageID == 0 || e.MessageID != s.MessageID || d.epoch != s.Epoch {
		return s, outdated()
	}
	if d.verb == "nop" {
		return s, nil
	}
	if s.Busy != "" {
		return s, []Action{Toast{Text: "⏳ still working: " + strings.TrimPrefix(s.Busy, "⏳ ")}}
	}
	s.Notice = ""
	switch d.verb {
	case "no":
		s.Confirm, s.Prompt, s.NewRepoName = nil, PromptNone, ""
		s.Epoch++
		return s, nil
	case "ok":
		return onConfirm(s, d)
	}
	switch s.Screen {
	case ScreenDash:
		return onDashTap(s, d)
	case ScreenIssues:
		return onIssuesTap(s, d)
	default:
		return onPickerTap(s, d)
	}
}

func onPickerTap(s State, d tapData) (State, []Action) {
	items := s.PickerItems()
	switch d.verb {
	case "r":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(items) {
			return s, outdated()
		}
		if items[i].Entry.Remote != nil {
			s.Confirm = &Confirm{Kind: ConfirmClone, Index: i}
			s.Epoch++
			return s, nil
		}
		return openDash(s, items[i].Entry.Repo)
	case "pg":
		delta, ok := d.intArg(0)
		if !ok {
			return s, outdated()
		}
		s.Page = clamp(s.Page+delta, 0, pageCount(len(items), PageSize)-1)
		return s, nil
	case "fg":
		s.Forge = nextForge(s.Forge, navdata.PresentForges(s.Local, s.Remote))
		s.Page = 0
		s.Epoch++
		return s, nil
	case "f":
		s.Prompt = PromptFilter
		s.Epoch++
		return s, nil
	case "fx":
		s.Filter, s.Page = "", 0
		s.Epoch++
		return s, nil
	case "nr":
		s.Prompt, s.NewRepoName = PromptNewRepoName, ""
		s.Epoch++
		return s, nil
	case "nc":
		i, ok := d.intArg(0)
		if !ok || s.Prompt != PromptNewRepoForge || i < 0 || i >= len(navdata.RepoChoices()) {
			return s, outdated()
		}
		s.Confirm = &Confirm{Kind: ConfirmNewRepo, Index: i}
		s.Epoch++
		return s, nil
	case "rf":
		s.Busy = busyRefreshRepos
		return s, []Action{LoadRepos{Refresh: true}}
	}
	return s, outdated()
}

// nextForge cycles All → each present forge → All.
func nextForge(cur string, present []navdata.ForgeOpt) string {
	if len(present) <= 1 {
		return ""
	}
	keys := []string{""}
	for _, o := range present {
		keys = append(keys, o.Key)
	}
	for i, k := range keys {
		if k == cur {
			return keys[(i+1)%len(keys)]
		}
	}
	return ""
}

func openDash(s State, repo core.Repo) (State, []Action) {
	s.Screen = ScreenDash
	s.Repo = repo
	s.Rows, s.Status = nil, nil
	s.Issues, s.IssuesLoaded, s.IssuePage = nil, false, 0
	s.Prompt, s.Confirm = PromptNone, nil
	s.Epoch++
	return s, []Action{LoadDash{Repo: repo}}
}

func onDashTap(s State, d tapData) (State, []Action) {
	switch d.verb {
	case "w":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(s.Rows) {
			return s, outdated()
		}
		row := s.Rows[i]
		s.Busy = "⏳ starting " + rowName(row) + "…"
		return s, []Action{Start{Repo: s.Repo, Worktree: row.Worktree, Label: row.DisplayLabel}}
	case "x":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(s.Rows) || !s.Rows[i].HasSession {
			return s, outdated()
		}
		s.Confirm = &Confirm{Kind: ConfirmStop, Index: i}
		s.Epoch++
		return s, nil
	case "nw":
		s.Prompt = PromptNewWorktree
		s.Epoch++
		return s, nil
	case "is":
		s.Screen, s.IssuePage = ScreenIssues, 0
		s.Epoch++
		return s, nil
	case "rf":
		s.Notice = noticeRefreshing
		return s, []Action{LoadDash{Repo: s.Repo}}
	case "bk":
		s.Screen, s.Prompt, s.Confirm = ScreenPicker, PromptNone, nil
		s.Epoch++
		return s, []Action{LoadRepos{}}
	}
	return s, outdated()
}

func onIssuesTap(s State, d tapData) (State, []Action) {
	switch d.verb {
	case "i":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(s.Issues) {
			return s, outdated()
		}
		s.Confirm = &Confirm{Kind: ConfirmIssue, Index: i}
		s.Epoch++
		return s, nil
	case "ip":
		delta, ok := d.intArg(0)
		if !ok {
			return s, outdated()
		}
		s.IssuePage = clamp(s.IssuePage+delta, 0, pageCount(len(s.Issues), IssuePageSize)-1)
		return s, nil
	case "bk":
		s.Screen = ScreenDash
		s.Epoch++
		return s, nil
	}
	return s, outdated()
}

// onConfirm handles ✅ "ok:<kind>:<index>", which must match the pending Confirm.
func onConfirm(s State, d tapData) (State, []Action) {
	i, ok := d.intArg(1)
	if s.Confirm == nil || len(d.args) != 2 || d.args[0] != string(s.Confirm.Kind) || !ok || i != s.Confirm.Index {
		return s, outdated()
	}
	var act Action
	busy := ""
	switch s.Confirm.Kind {
	case ConfirmClone:
		items := s.PickerItems()
		if i >= len(items) || items[i].Entry.Remote == nil {
			return s, outdated()
		}
		ref := *items[i].Entry.Remote
		act, busy = Clone{Ref: ref}, "⏳ cloning "+ref.Name+"…"
	case ConfirmNewRepo:
		choices := navdata.RepoChoices()
		if i >= len(choices) || s.NewRepoName == "" {
			return s, outdated()
		}
		ch := choices[i]
		act = CreateRepo{Name: s.NewRepoName, Forge: ch.Forge, Private: ch.Private}
		busy = "⏳ creating " + s.NewRepoName + " (" + ch.Label + ")…"
	case ConfirmStop:
		if i >= len(s.Rows) || !s.Rows[i].HasSession {
			return s, outdated()
		}
		slot := s.Rows[i].SlotID
		act, busy = Stop{Slot: slot}, "⏳ stopping "+slot+"…"
	case ConfirmIssue:
		if i >= len(s.Issues) {
			return s, outdated()
		}
		is := s.Issues[i]
		wt, label := navdata.IssueWorktreeName(is.Number, is.Title)
		act = Start{Repo: s.Repo, Worktree: wt, Label: label, Create: true}
		busy = fmt.Sprintf("⏳ starting #%d…", is.Number)
	default:
		return s, outdated()
	}
	s.Confirm, s.Busy = nil, busy
	s.Epoch++
	return s, []Action{act}
}

func onReposLoaded(s State, e ReposLoaded) State {
	if s.Busy == busyRefreshRepos {
		s.Busy = ""
	}
	if e.Err != nil {
		s.Notice = "⚠ " + shortErr(e.Err)
	}
	s.Local, s.Remote, s.MRU, s.ReposLoaded = e.Local, e.Remote, e.MRU, true
	present := false
	for _, o := range navdata.PresentForges(s.Local, s.Remote) {
		present = present || o.Key == s.Forge
	}
	if !present {
		s.Forge = ""
	}
	s.Page = clamp(s.Page, 0, pageCount(len(s.PickerItems()), PageSize)-1)
	if s.Screen == ScreenPicker {
		// Indexes moved: a pending clone confirm may now point elsewhere.
		if s.Confirm != nil && s.Confirm.Kind == ConfirmClone {
			s.Confirm = nil
		}
		s.Epoch++
	}
	return s
}

// current reports whether a result for repo still belongs on screen.
func (s State) current(repo core.Repo) bool {
	return s.Screen != ScreenPicker && repo.Path == s.Repo.Path
}

func rowPaths(rows []navdata.DashRow) []string {
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	return paths
}

func onDashLoaded(s State, e DashLoaded) (State, []Action) {
	if !s.current(e.Repo) {
		return s, nil
	}
	s.Rows, s.Status = e.Rows, map[string]RowStatus{}
	if s.Notice == noticeRefreshing {
		s.Notice = ""
	}
	if s.Screen == ScreenDash {
		if s.Confirm != nil && s.Confirm.Kind == ConfirmStop {
			s.Confirm = nil
		}
		s.Epoch++
	}
	acts := []Action{LoadStatus{Repo: s.Repo, Paths: rowPaths(e.Rows)}, Fetch{Repo: s.Repo}}
	if !s.IssuesLoaded && hasForgeIDs(s.Repo) {
		acts = append(acts, LoadIssues{Repo: s.Repo})
	}
	return s, acts
}

func onStatusLoaded(s State, e StatusLoaded) State {
	if !s.current(e.Repo) {
		return s
	}
	m := make(map[string]RowStatus, len(s.Status)+len(e.Status))
	for k, v := range s.Status {
		m[k] = v
	}
	for k, v := range e.Status {
		m[k] = v
	}
	s.Status = m
	return s
}

func onFetchDone(s State, e FetchDone) (State, []Action) {
	if !s.current(e.Repo) || e.Err != nil || s.Rows == nil {
		return s, nil // offline / failed fetch: keep last-known status
	}
	return s, []Action{LoadStatus{Repo: s.Repo, Paths: rowPaths(s.Rows)}}
}

func onIssuesLoaded(s State, e IssuesLoaded) State {
	if !s.current(e.Repo) {
		return s
	}
	s.Issues, s.IssuesLoaded = e.Issues, true
	s.IssuePage = clamp(s.IssuePage, 0, pageCount(len(s.Issues), IssuePageSize)-1)
	if s.Screen == ScreenIssues {
		if s.Confirm != nil && s.Confirm.Kind == ConfirmIssue {
			s.Confirm = nil
		}
		s.Epoch++
	}
	return s
}

func onStarted(s State, e Started) (State, []Action) {
	s.Busy = ""
	var acts []Action
	switch {
	case e.Err != nil:
		s.Notice = "⚠ " + shortErr(e.Err)
	case e.RCURL != "":
		s.Notice = "launched " + e.Slot
		if e.AlreadyRunning {
			s.Notice = "already running: " + e.Slot
		}
		acts = append(acts, SendLink{Text: "🔗 " + e.Slot + "\n" + e.RCURL})
	case e.Exited:
		s.Notice = "⚠ session " + e.Slot + " exited during startup"
	case e.AlreadyRunning:
		s.Notice = "already running: " + e.Slot + " (no link yet)"
	default:
		s.Notice = "session " + e.Slot + " running, no link yet"
	}
	if s.Screen == ScreenIssues {
		s.Screen = ScreenDash
		s.Epoch++
	}
	if s.Screen == ScreenDash {
		acts = append(acts, LoadDash{Repo: s.Repo})
	}
	return s, acts
}

func onStopped(s State, e Stopped) (State, []Action) {
	s.Busy = ""
	if e.Err != nil {
		s.Notice = "⚠ " + shortErr(e.Err)
	} else {
		s.Notice = "stopped " + e.Slot
	}
	if s.Screen == ScreenDash {
		return s, []Action{LoadDash{Repo: s.Repo}}
	}
	return s, nil
}

// onOpened finishes a clone or a repo creation: open the new repo's
// dashboard and reload the picker lists (it is local now).
func onOpened(s State, repo core.Repo, err error) (State, []Action) {
	s.Busy = ""
	if err != nil {
		s.Notice = "⚠ " + shortErr(err)
		return s, nil
	}
	s, acts := openDash(s, repo)
	s.Notice = "ready: " + repo.Name
	return s, append(acts, LoadRepos{})
}

// shortErr is the first line of err without the "bridge: " prefix, ≤200 runes.
func shortErr(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return navdata.TruncRunes(strings.TrimPrefix(msg, "bridge: "), 200)
}

// rowName is a dashboard row's short name: the base row's branch (or "base"),
// else the worktree basename.
func rowName(r navdata.DashRow) string {
	if r.IsBase {
		if r.Branch != "" {
			return r.Branch
		}
		return "base"
	}
	return r.Worktree
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal/bot && go vet ./internal/bot && go test -race ./internal/bot/`
Expected: `ok  	github.com/freaxnx01/bridge/internal/bot`

- [ ] **Step 5: Commit**

```bash
git add internal/bot
git commit -m "feat(bot): pure screen state machine for the Telegram nav bot"
```

---

### Task 6: `internal/bot` — `Render` with golden tests

**Files:**
- Create: `internal/bot/render.go`
- Test: `internal/bot/render_test.go`, `internal/bot/testdata/picker.golden`, `dash.golden`, `dash_confirm_stop.golden`, `dash_zero_worktrees.golden`, `issues_emoji.golden`, `new_repo_forge.golden`

**Interfaces:**
- Consumes (Task 5): `State`, `PickerItem`, `Confirm`, `ConfirmKind` values, `RowStatus`, `Prompt` values, `Screen` values, `PageSize`, `IssuePageSize`, `MaxCallbackBytes`, `data`, `pageCount`, `clamp`, `hasForgeIDs`, `rowName`. (Tasks 1–2): `navdata.PresentForges`, `navdata.Parts`, `navdata.RepoChoices`, `navdata.IssueWorktreeName`, `navdata.TruncRunes`, `navdata.RemoteEntries`, `navdata.Dirty`.
- Produces: `type Button struct{ Text, Data string }` (JSON tags `text` / `callback_data`), `type Keyboard [][]Button`, `func Render(s State) (string, Keyboard)` — used by the runner in Task 7. Texts are plain (no parse mode), ≤ 4000 runes.

- [ ] **Step 1: Write the failing test and its golden files**

Create `internal/bot/render_test.go`:

```go
package bot

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

// golden compares Render(s) with testdata/<name>.golden: the text, a
// separator line, then the keyboard as indented JSON.
func golden(t *testing.T, name string, s State) {
	t.Helper()
	text, kb := Render(s)
	js, err := json.MarshalIndent(kb, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := text + "\n--- keyboard ---\n" + string(js) + "\n"
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

var bridgeRepo = core.Repo{Name: "bridge", Forge: "github", Owner: "freaxnx01", Visibility: "public", Path: "/r/github/bridge"}

func goldenPicker() State {
	return State{
		MessageID: 10, Epoch: 3, ReposLoaded: true,
		Local: []navdata.RepoEntry{
			{Label: "github/public/bridge", Repo: bridgeRepo},
			{Label: "forgejo/config", Repo: core.Repo{Name: "config", Forge: "forgejo", Owner: "freaxnx01", Path: "/r/git-forgejo/config"}},
			{Label: "github/public/agent-workflow", Repo: core.Repo{Name: "agent-workflow", Forge: "github", Owner: "freaxnx01", Visibility: "public", Path: "/r/github/agent-workflow"}},
		},
		Remote: navdata.RemoteEntries([]forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "obsidian-it", Visibility: "private"}}),
		MRU:    []string{"/r/github/bridge"},
	}
}

func goldenDash() State {
	return State{
		MessageID: 10, Epoch: 5, Screen: ScreenDash, Repo: bridgeRepo,
		Rows: []navdata.DashRow{
			{IsBase: true, Branch: "main", Path: "/r/github/bridge", HasSession: true, SlotID: "bridge", State: "attached"},
			{Worktree: "tg-admin", Branch: "worktree-tg-admin", Path: "/r/github/bridge/.worktrees/tg-admin"},
			{Worktree: "reorg", Branch: "worktree-reorg", Path: "/r/github/bridge/.worktrees/reorg", HasSession: true, SlotID: "bridge-wt-reorg", State: "detached"},
		},
		Status: map[string]RowStatus{
			"/r/github/bridge":                     {Dirty: navdata.Dirty{Clean: true}},
			"/r/github/bridge/.worktrees/tg-admin": {Dirty: navdata.Dirty{Modified: 3, Ahead: 2}},
			"/r/github/bridge/.worktrees/reorg":    {Dirty: navdata.Dirty{Behind: 1, Clean: true}},
		},
		IssuesLoaded: true,
		Issues: []forge.Issue{
			{Number: 312, Title: "fix nav esc"}, {Number: 305, Title: "bot: paging"},
			{Number: 300, Title: "x"}, {Number: 299, Title: "y"},
		},
		Notice: "launched bridge-wt-reorg",
	}
}

func TestRenderGolden(t *testing.T) {
	golden(t, "picker", goldenPicker())

	golden(t, "dash", goldenDash())

	stop := goldenDash()
	stop.Notice, stop.Confirm = "", &Confirm{Kind: ConfirmStop, Index: 2}
	golden(t, "dash_confirm_stop", stop)

	// Review Focus: a repo with zero worktrees still shows its base row.
	golden(t, "dash_zero_worktrees", State{
		MessageID: 10, Epoch: 2, Screen: ScreenDash,
		Repo: core.Repo{Name: "notes", Path: "/r/notes"},
		Rows: []navdata.DashRow{{IsBase: true, Branch: "main", Path: "/r/notes"}},
	})

	// Review Focus: emoji + slash in an issue title.
	golden(t, "issues_emoji", State{
		MessageID: 10, Epoch: 2, Screen: ScreenIssues, Repo: bridgeRepo, IssuesLoaded: true,
		Issues: []forge.Issue{{Number: 7, Title: "🚀 fix/launch: crash"}},
	})

	golden(t, "new_repo_forge", State{
		MessageID: 10, Epoch: 4, ReposLoaded: true, Prompt: PromptNewRepoForge, NewRepoName: "demo",
	})
}

// Review Focus companion: two "bridge" repos on different forges get distinct
// button labels.
func TestPickerButtons_SameNameDisambiguated(t *testing.T) {
	s := State{MessageID: 1, Epoch: 1, ReposLoaded: true, Local: []navdata.RepoEntry{
		{Label: "github/public/bridge", Repo: bridgeRepo},
		{Label: "forgejo/bridge", Repo: core.Repo{Name: "bridge", Forge: "forgejo", Owner: "freaxnx01", Path: "/r/git-forgejo/bridge"}},
	}}
	_, kb := Render(s)
	var texts []string
	for _, row := range kb {
		for _, b := range row {
			if strings.Contains(b.Data, ":r:") {
				texts = append(texts, b.Text)
			}
		}
	}
	if strings.Join(texts, "|") != "bridge · forgejo|bridge · github" {
		t.Errorf("buttons = %v", texts)
	}
}

// Every button of every screen fits Telegram's 64-byte callback limit and is
// valid UTF-8, even with huge indexes and epochs and hostile titles.
func TestRender_CallbackDataFitsAndTextIsValidUTF8(t *testing.T) {
	var many []navdata.RepoEntry
	for i := 0; i < 2000; i++ {
		many = append(many, navdata.RepoEntry{Label: fmt.Sprintf("github/public/r%04d", i),
			Repo: core.Repo{Name: fmt.Sprintf("r%04d", i), Forge: "github", Path: fmt.Sprintf("/r/%d", i)}})
	}
	var issues []forge.Issue
	for i := 0; i < 500; i++ {
		issues = append(issues, forge.Issue{Number: 100000 + i, Title: strings.Repeat("🚀/ä\n", 40)})
	}
	rows := make([]navdata.DashRow, 300)
	for i := range rows {
		rows[i] = navdata.DashRow{Worktree: strings.Repeat("w", 40) + fmt.Sprint(i), HasSession: true, SlotID: "s", State: "attached"}
	}
	states := []State{
		{Epoch: 1 << 30, ReposLoaded: true, Local: many, Page: 249},
		{Epoch: 1 << 30, Screen: ScreenDash, Repo: bridgeRepo, Rows: rows, Issues: issues, IssuesLoaded: true},
		{Epoch: 1 << 30, Screen: ScreenIssues, Repo: bridgeRepo, Issues: issues, IssuesLoaded: true, IssuePage: 62},
		{Epoch: 1 << 30, Screen: ScreenIssues, Repo: bridgeRepo, Issues: issues, IssuesLoaded: true, Confirm: &Confirm{ConfirmIssue, 499}},
		{Epoch: 1 << 30, Screen: ScreenDash, Repo: bridgeRepo, Rows: rows, Confirm: &Confirm{ConfirmStop, 299}},
	}
	for i, s := range states {
		text, kb := Render(s)
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxTextRunes {
			t.Errorf("state %d: text invalid or too long (%d runes)", i, utf8.RuneCountInString(text))
		}
		for _, row := range kb {
			for _, b := range row {
				if len(b.Data) > MaxCallbackBytes || !utf8.ValidString(b.Text) || strings.Contains(b.Text, "\n") {
					t.Errorf("state %d: bad button %q / %q (%d bytes)", i, b.Text, b.Data, len(b.Data))
				}
			}
		}
	}
}

func TestRender_LoadingAndOverlays(t *testing.T) {
	text, kb := Render(State{Epoch: 1})
	if !strings.Contains(text, "⏳ loading repos…") || len(kb) != 1 {
		t.Errorf("loading picker: %q %v", text, kb)
	}
	text, kb = Render(State{Epoch: 1, ReposLoaded: true, Prompt: PromptFilter})
	if !strings.Contains(text, "✏ Send the filter text") || len(kb) != 1 || kb[0][0].Data != "1:no" {
		t.Errorf("filter prompt: %q %v", text, kb)
	}
	text, _ = Render(State{Epoch: 1, Screen: ScreenDash, Repo: bridgeRepo, Busy: "⏳ starting main…"})
	if !strings.HasSuffix(text, "\n⏳ starting main…") || !strings.Contains(text, "⏳ loading…") {
		t.Errorf("busy dash: %q", text)
	}
}
```

Create the golden files with exactly this content (each ends with a newline):

`internal/bot/testdata/picker.golden`:

```text
🗂 bridge · repos · All
4 repos · page 1/1
--- keyboard ---
[
  [
    {
      "text": "All ▸",
      "callback_data": "3:fg"
    }
  ],
  [
    {
      "text": "⭐ bridge",
      "callback_data": "3:r:0"
    },
    {
      "text": "agent-workflow",
      "callback_data": "3:r:1"
    }
  ],
  [
    {
      "text": "config",
      "callback_data": "3:r:2"
    },
    {
      "text": "↓ obsidian-it",
      "callback_data": "3:r:3"
    }
  ],
  [
    {
      "text": "🔍 Filter",
      "callback_data": "3:f"
    },
    {
      "text": "➕ New repo",
      "callback_data": "3:nr"
    },
    {
      "text": "🔄",
      "callback_data": "3:rf"
    }
  ]
]
```

`internal/bot/testdata/dash.golden`:

```text
📁 bridge · github/freaxnx01
main  ● attached  ✓
tg-admin  ○  ✎3 ↑2
reorg  ● detached  ↓1
Issues: #312 fix nav esc · #305 bot: paging (+2)
launched bridge-wt-reorg
--- keyboard ---
[
  [
    {
      "text": "main ▶",
      "callback_data": "5:w:0"
    },
    {
      "text": "tg-admin ▶",
      "callback_data": "5:w:1"
    },
    {
      "text": "reorg ▶",
      "callback_data": "5:w:2"
    }
  ],
  [
    {
      "text": "main ⏹",
      "callback_data": "5:x:0"
    },
    {
      "text": "reorg ⏹",
      "callback_data": "5:x:2"
    }
  ],
  [
    {
      "text": "➕ Worktree",
      "callback_data": "5:nw"
    },
    {
      "text": "🐞 Issues",
      "callback_data": "5:is"
    }
  ],
  [
    {
      "text": "🔄",
      "callback_data": "5:rf"
    },
    {
      "text": "◀ Repos",
      "callback_data": "5:bk"
    }
  ]
]
```

`internal/bot/testdata/dash_confirm_stop.golden`:

```text
📁 bridge · github/freaxnx01
main  ● attached  ✓
tg-admin  ○  ✎3 ↑2
reorg  ● detached  ↓1
Issues: #312 fix nav esc · #305 bot: paging (+2)

Stop bridge-wt-reorg?
--- keyboard ---
[
  [
    {
      "text": "✅ Yes",
      "callback_data": "5:ok:stop:2"
    },
    {
      "text": "✖ No",
      "callback_data": "5:no"
    }
  ]
]
```

`internal/bot/testdata/dash_zero_worktrees.golden`:

```text
📁 notes · /r/notes
main  ○  …
Issues: —
--- keyboard ---
[
  [
    {
      "text": "main ▶",
      "callback_data": "2:w:0"
    }
  ],
  [
    {
      "text": "➕ Worktree",
      "callback_data": "2:nw"
    },
    {
      "text": "🐞 Issues",
      "callback_data": "2:is"
    }
  ],
  [
    {
      "text": "🔄",
      "callback_data": "2:rf"
    },
    {
      "text": "◀ Repos",
      "callback_data": "2:bk"
    }
  ]
]
```

`internal/bot/testdata/issues_emoji.golden`:

```text
🐞 bridge · open issues (1) · page 1/1
--- keyboard ---
[
  [
    {
      "text": "#7 🚀 fix/launch: crash",
      "callback_data": "2:i:0"
    }
  ],
  [
    {
      "text": "◀ Dashboard",
      "callback_data": "2:bk"
    }
  ]
]
```

`internal/bot/testdata/new_repo_forge.golden`:

```text
🗂 bridge · repos · All
0 repos · page 1/1

Where should demo live?
--- keyboard ---
[
  [
    {
      "text": "Forgejo · Private",
      "callback_data": "4:nc:0"
    }
  ],
  [
    {
      "text": "Forgejo · Public",
      "callback_data": "4:nc:1"
    }
  ],
  [
    {
      "text": "GitHub · Private",
      "callback_data": "4:nc:2"
    }
  ],
  [
    {
      "text": "GitHub · Public",
      "callback_data": "4:nc:3"
    }
  ],
  [
    {
      "text": "✖ Cancel",
      "callback_data": "4:no"
    }
  ]
]
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bot/`
Expected: FAIL — `undefined: Render`, `undefined: maxTextRunes`.

- [ ] **Step 3: Implement**

Create `internal/bot/render.go`:

```go
package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/navdata"
)

// Button is an inline-keyboard callback button.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Keyboard is rows of buttons.
type Keyboard [][]Button

const (
	maxTextRunes   = 4000 // Telegram's message limit is 4096
	maxButtonRunes = 32
)

// Render returns the message text (plain, no parse mode) and keyboard for s.
// A pending confirm or prompt replaces the keyboard with its own buttons.
func Render(s State) (string, Keyboard) {
	var text string
	var kb Keyboard
	switch s.Screen {
	case ScreenDash:
		text, kb = renderDash(s)
	case ScreenIssues:
		text, kb = renderIssues(s)
	default:
		text, kb = renderPicker(s)
	}
	if q, okb := renderOverlay(s); q != "" {
		text, kb = text+"\n\n"+q, okb
	}
	for _, line := range []string{s.Notice, s.Busy} {
		if line != "" {
			text += "\n" + line
		}
	}
	if kb == nil {
		kb = Keyboard{}
	}
	return navdata.TruncRunes(text, maxTextRunes), kb
}

func btn(text string, epoch int, verb string, args ...string) Button {
	return Button{Text: text, Data: data(epoch, verb, args...)}
}

// oneLine collapses whitespace (titles may hold newlines) for a button label.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func renderPicker(s State) (string, Keyboard) {
	var b strings.Builder
	present := navdata.PresentForges(s.Local, s.Remote)
	forgeLabel := "All"
	for _, o := range present {
		if o.Key == s.Forge {
			forgeLabel = o.Label
		}
	}
	fmt.Fprintf(&b, "🗂 bridge · repos · %s", forgeLabel)
	if !s.ReposLoaded {
		b.WriteString("\n⏳ loading repos…")
		return b.String(), Keyboard{{btn("🔄", s.Epoch, "rf")}}
	}
	items := s.PickerItems()
	if s.Filter != "" {
		fmt.Fprintf(&b, "\n🔍 %q", s.Filter)
	}
	pages := pageCount(len(items), PageSize)
	page := clamp(s.Page, 0, pages-1)
	fmt.Fprintf(&b, "\n%d repos · page %d/%d", len(items), page+1, pages)

	var kb Keyboard
	if len(present) > 1 {
		kb = append(kb, []Button{btn(forgeLabel+" ▸", s.Epoch, "fg")})
	}
	var row []Button
	for i := page * PageSize; i < min((page+1)*PageSize, len(items)); i++ {
		row = append(row, btn(pickerButtonText(items, i), s.Epoch, "r", strconv.Itoa(i)))
		if len(row) == 2 {
			kb, row = append(kb, row), nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	if pages > 1 {
		kb = append(kb, []Button{
			btn("◀", s.Epoch, "pg", "-1"),
			btn(fmt.Sprintf("%d/%d", page+1, pages), s.Epoch, "nop"),
			btn("▶", s.Epoch, "pg", "+1"),
		})
	}
	filter := btn("🔍 Filter", s.Epoch, "f")
	if s.Filter != "" {
		filter = btn("✖ Filter", s.Epoch, "fx")
	}
	kb = append(kb, []Button{filter, btn("➕ New repo", s.Epoch, "nr"), btn("🔄", s.Epoch, "rf")})
	return b.String(), kb
}

// pickerButtonText is the repo name, qualified by forge (and owner) only when
// the name is ambiguous in the list, prefixed ⭐ (MRU) or ↓ (remote).
func pickerButtonText(items []PickerItem, i int) string {
	f, owner, _, name := navdata.Parts(items[i].Entry)
	byName, byNameForge := 0, 0
	for _, it := range items {
		f2, _, _, n2 := navdata.Parts(it.Entry)
		if strings.EqualFold(n2, name) {
			byName++
			if f2 == f {
				byNameForge++
			}
		}
	}
	label := name
	switch {
	case byNameForge > 1:
		label += " · " + f + "/" + owner
	case byName > 1:
		label += " · " + f
	}
	switch {
	case items[i].Star:
		label = "⭐ " + label
	case items[i].Entry.Remote != nil:
		label = "↓ " + label
	}
	return navdata.TruncRunes(label, maxButtonRunes)
}

func repoWhere(r core.Repo) string {
	switch {
	case r.Forge != "" && r.Owner != "":
		return r.Forge + "/" + r.Owner
	case r.Forge != "":
		return r.Forge
	}
	return r.Path
}

func renderDash(s State) (string, Keyboard) {
	var b strings.Builder
	fmt.Fprintf(&b, "📁 %s · %s", s.Repo.Name, repoWhere(s.Repo))
	bottom := Keyboard{
		{btn("➕ Worktree", s.Epoch, "nw"), btn("🐞 Issues", s.Epoch, "is")},
		{btn("🔄", s.Epoch, "rf"), btn("◀ Repos", s.Epoch, "bk")},
	}
	if s.Rows == nil {
		b.WriteString("\n⏳ loading…")
		return b.String(), bottom
	}
	var starts, stops []Button
	for i, r := range s.Rows {
		name := navdata.TruncRunes(rowName(r), 24)
		sess := "○"
		if r.HasSession {
			sess = "● " + r.State
		}
		fmt.Fprintf(&b, "\n%s  %s  %s", name, sess, gitCell(s.Status, r.Path))
		starts = append(starts, btn(name+" ▶", s.Epoch, "w", strconv.Itoa(i)))
		if r.HasSession {
			stops = append(stops, btn(name+" ⏹", s.Epoch, "x", strconv.Itoa(i)))
		}
	}
	b.WriteString("\n" + issuesSummary(s))
	var kb Keyboard
	kb = appendGrid(kb, starts, 3)
	kb = appendGrid(kb, stops, 3)
	return b.String(), append(kb, bottom...)
}

func appendGrid(kb Keyboard, buttons []Button, perRow int) Keyboard {
	for len(buttons) > 0 {
		n := min(perRow, len(buttons))
		kb = append(kb, buttons[:n])
		buttons = buttons[n:]
	}
	return kb
}

// gitCell: … pending, ? unreadable, ✎N modified, ∅ no upstream, ↑ahead ↓behind, ✓ clean.
func gitCell(st map[string]RowStatus, path string) string {
	rs, ok := st[path]
	switch {
	case !ok:
		return "…"
	case rs.Err:
		return "?"
	}
	d := rs.Dirty
	var t []string
	if d.Modified > 0 {
		t = append(t, fmt.Sprintf("✎%d", d.Modified))
	}
	if d.NoUpstream {
		t = append(t, "∅")
	} else {
		if d.Ahead > 0 {
			t = append(t, fmt.Sprintf("↑%d", d.Ahead))
		}
		if d.Behind > 0 {
			t = append(t, fmt.Sprintf("↓%d", d.Behind))
		}
	}
	if len(t) == 0 {
		return "✓"
	}
	return strings.Join(t, " ")
}

func issuesSummary(s State) string {
	switch {
	case !hasForgeIDs(s.Repo):
		return "Issues: —"
	case !s.IssuesLoaded:
		return "Issues: loading…"
	case len(s.Issues) == 0:
		return "Issues: none"
	}
	const show = 2
	var parts []string
	for i := 0; i < min(show, len(s.Issues)); i++ {
		parts = append(parts, fmt.Sprintf("#%d %s", s.Issues[i].Number, navdata.TruncRunes(oneLine(s.Issues[i].Title), 24)))
	}
	out := "Issues: " + strings.Join(parts, " · ")
	if len(s.Issues) > show {
		out += fmt.Sprintf(" (+%d)", len(s.Issues)-show)
	}
	return out
}

func renderIssues(s State) (string, Keyboard) {
	var b strings.Builder
	fmt.Fprintf(&b, "🐞 %s · open issues", s.Repo.Name)
	back := []Button{btn("◀ Dashboard", s.Epoch, "bk")}
	switch {
	case !hasForgeIDs(s.Repo):
		b.WriteString("\nno forge issues for this repo")
		return b.String(), Keyboard{back}
	case !s.IssuesLoaded:
		b.WriteString("\n⏳ loading…")
		return b.String(), Keyboard{back}
	case len(s.Issues) == 0:
		b.WriteString("\nnone open")
		return b.String(), Keyboard{back}
	}
	pages := pageCount(len(s.Issues), IssuePageSize)
	page := clamp(s.IssuePage, 0, pages-1)
	fmt.Fprintf(&b, " (%d) · page %d/%d", len(s.Issues), page+1, pages)
	kb := Keyboard{}
	for i := page * IssuePageSize; i < min((page+1)*IssuePageSize, len(s.Issues)); i++ {
		is := s.Issues[i]
		label := navdata.TruncRunes(oneLine(fmt.Sprintf("#%d %s", is.Number, is.Title)), 48)
		kb = append(kb, []Button{btn(label, s.Epoch, "i", strconv.Itoa(i))})
	}
	if pages > 1 {
		kb = append(kb, []Button{
			btn("◀", s.Epoch, "ip", "-1"),
			btn(fmt.Sprintf("%d/%d", page+1, pages), s.Epoch, "nop"),
			btn("▶", s.Epoch, "ip", "+1"),
		})
	}
	return b.String(), append(kb, back)
}

// renderOverlay returns the question and keyboard of a pending confirm or
// prompt, or "" when there is none.
func renderOverlay(s State) (string, Keyboard) {
	cancel := []Button{btn("✖ Cancel", s.Epoch, "no")}
	if c := s.Confirm; c != nil {
		return confirmQuestion(s, *c), Keyboard{{
			btn("✅ Yes", s.Epoch, "ok", string(c.Kind), strconv.Itoa(c.Index)),
			btn("✖ No", s.Epoch, "no"),
		}}
	}
	switch s.Prompt {
	case PromptFilter:
		return "✏ Send the filter text (case-insensitive substring).", Keyboard{cancel}
	case PromptNewRepoName:
		return "✏ Send the new repo name (A-Z a-z 0-9 . _ -, max 40).", Keyboard{cancel}
	case PromptNewWorktree:
		return "✏ Send the new worktree name (A-Z a-z 0-9 . _ -, max 40).", Keyboard{cancel}
	case PromptNewRepoForge:
		kb := Keyboard{}
		for i, ch := range navdata.RepoChoices() {
			kb = append(kb, []Button{btn(ch.Label, s.Epoch, "nc", strconv.Itoa(i))})
		}
		return "Where should " + s.NewRepoName + " live?", append(kb, cancel)
	}
	return "", nil
}

func confirmQuestion(s State, c Confirm) string {
	switch c.Kind {
	case ConfirmClone:
		if items := s.PickerItems(); c.Index < len(items) {
			return "Clone " + strings.TrimPrefix(items[c.Index].Entry.Label, "↓ ") + "?"
		}
	case ConfirmStop:
		if c.Index < len(s.Rows) {
			return "Stop " + s.Rows[c.Index].SlotID + "?"
		}
	case ConfirmIssue:
		if c.Index < len(s.Issues) {
			is := s.Issues[c.Index]
			wt, label := navdata.IssueWorktreeName(is.Number, is.Title)
			return "Start " + label + "?\nworktree: " + wt
		}
	case ConfirmNewRepo:
		if ch := navdata.RepoChoices(); c.Index < len(ch) {
			return "Create " + s.NewRepoName + " on " + ch[c.Index].Label + "?"
		}
	}
	return "Confirm?"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal/bot && go vet ./internal/bot && go test -race ./internal/bot/`
Expected: `ok  	github.com/freaxnx01/bridge/internal/bot`. If a golden differs, fix `render.go`, not the golden; only regenerate (`go test ./internal/bot -run TestRenderGolden -update`) for a deliberate layout change, and review the diff.

- [ ] **Step 5: Commit**

```bash
git add internal/bot/render.go internal/bot/render_test.go internal/bot/testdata
git commit -m "feat(bot): render screens as text and inline keyboards"
```

---

### Task 7: `bridge bot` runner, config and E2E

**Files:**
- Create: `cmd/bridge/bot.go` (`//go:build !windows`), `cmd/bridge/bot_env.go` (`//go:build !windows`), `cmd/bridge/bot_windows.go` (`//go:build windows`)
- Modify: `cmd/bridge/nav.go` (extract `fetchOpenIssues`)
- Test: `cmd/bridge/bot_config_test.go`, `cmd/bridge/bot_test.go` (both `//go:build !windows`)

**Interfaces:**
- Consumes: Task 4 `telegram.New`, `telegram.WithBaseURL`, `(*telegram.Client).GetUpdates/SendMessage/EditMessageText/AnswerCallbackQuery`, `telegram.ErrConflict`, `telegram.ErrNotModified`, `telegram.Update`, `telegram.InlineKeyboardMarkup`, `telegram.InlineKeyboardButton`. Task 5/6 `bot.Config`, `bot.State`, `bot.Update`, `bot.Render`, `bot.Keyboard`, all `bot` events/actions listed in Task 5. Task 3 `launch.TargetFor`, `launch.StartDetached`, `launch.WaitRCURL`, `launch.SessionLive`, `launch.Stop`, `launchDeps()`. Tasks 1–2 `navdata.LocalRepos`, `RemoteRepos`, `RemoteEntries`, `RecentPaths`, `DashRows`, `GitDirty`, `GitFetch`, `IssuesCached`, `IssueSource`, `CreateWorktree`, `WorktreeErrMessage`, `CloneFunc`, `CreateRepoFunc`. Existing `cmd/bridge`: `cacheRoot`, `reposRoots`, `reposRoot`, `loadSessions`, `cloneRemoteRepo`, `repoFromClonedRef`, `createAndClone`, `clientFor`, `remote.Refresh`. Test fixtures: `bridgeCmd`, `fakeTmux`, `launchEnv`, `writeFakeRepos` (via `launchEnv`).
- Produces: the `bridge bot [--config path]` command; `func fetchOpenIssues(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error)` (nav.go, portable); `type botConfig struct{ Token string; Allow []int64; APIURL string }`, `func loadBotConfig(path string) (botConfig, error)`, `func defaultBotConfigPath(getenv func(string) string) string`. `bot.env` keys: `TELEGRAM_BOT_TOKEN`, `BRIDGE_BOT_ALLOW`, optional `BRIDGE_BOT_API_URL`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/bridge/bot_config_test.go`:

```go
//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeBotEnvFile(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bot.env")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // umask-proof
		t.Fatal(err)
	}
	return path
}

func TestLoadBotConfig_Parses(t *testing.T) {
	path := writeBotEnvFile(t, 0o600, "# bridge bot\nexport TELEGRAM_BOT_TOKEN=\"123:abc\"\nBRIDGE_BOT_ALLOW= 111, 222 \n\nBRIDGE_BOT_API_URL='http://127.0.0.1:9'\n")
	cfg, err := loadBotConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := botConfig{Token: "123:abc", Allow: []int64{111, 222}, APIURL: "http://127.0.0.1:9"}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
}

// Review Focus: a token file readable by others is refused, and the refusal
// does not echo the token.
func TestLoadBotConfig_RefusesGroupOrWorldReadable(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		path := writeBotEnvFile(t, mode, "TELEGRAM_BOT_TOKEN=123:SECRET\nBRIDGE_BOT_ALLOW=1\n")
		_, err := loadBotConfig(path)
		if err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %04o: err = %v, want a chmod 600 refusal", mode, err)
			continue
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("mode %04o: error leaks the token: %v", mode, err)
		}
	}
}

func TestLoadBotConfig_Errors(t *testing.T) {
	cases := map[string]string{
		"no token":    "BRIDGE_BOT_ALLOW=1\n",
		"no allow":    "TELEGRAM_BOT_TOKEN=1:x\n",
		"bad allow":   "TELEGRAM_BOT_TOKEN=1:x\nBRIDGE_BOT_ALLOW=@me\n",
		"empty allow": "TELEGRAM_BOT_TOKEN=1:x\nBRIDGE_BOT_ALLOW= , \n",
	}
	for name, body := range cases {
		if _, err := loadBotConfig(writeBotEnvFile(t, 0o600, body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := loadBotConfig(filepath.Join(t.TempDir(), "missing.env")); err == nil || !strings.Contains(err.Error(), "docs/telegram-bot.md") {
		t.Errorf("missing file: err = %v", err)
	}
}

func TestDefaultBotConfigPath(t *testing.T) {
	got := defaultBotConfigPath(func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return "/x"
		}
		return ""
	})
	if got != "/x/bridge/bot.env" {
		t.Errorf("got %s", got)
	}
}
```

Create `cmd/bridge/bot_test.go` (end-to-end: the built binary against a fake Telegram server and the existing fake tmux; `writeFakeRepos` repos are not git checkouts, so the dashboard shows only the base row, named `base`):

```go
//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeBotToken = "42:FAKE-TOKEN"

// tgCall is one sendMessage/editMessageText the bot made.
type tgCall struct {
	Method    string
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
	Text      string
	Markup    struct {
		Rows [][]struct {
			Text string `json:"text"`
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	} `json:"reply_markup"`
}

// fakeTelegram is a scripted Bot API: tests queue updates, the bot polls them
// and its replies are recorded.
type fakeTelegram struct {
	t       *testing.T
	mu      sync.Mutex
	queue   []map[string]any
	nextID  int64
	nextMsg int
	calls   []tgCall
	answers int
	srv     *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{t: t, nextID: 1, nextMsg: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	prefix := "/bot" + fakeBotToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	raw, _ := io.ReadAll(r.Body)
	reply := func(result any) {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		_, _ = w.Write(b)
	}
	switch method {
	case "getUpdates":
		var req struct {
			Offset int64 `json:"offset"`
		}
		_ = json.Unmarshal(raw, &req)
		deadline := time.Now().Add(300 * time.Millisecond)
		for {
			f.mu.Lock()
			var out []map[string]any
			for _, u := range f.queue {
				if u["update_id"].(int64) >= req.Offset {
					out = append(out, u)
				}
			}
			f.mu.Unlock()
			if len(out) > 0 || time.Now().After(deadline) {
				if out == nil {
					out = []map[string]any{}
				}
				reply(out)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "sendMessage", "editMessageText":
		var c tgCall
		if err := json.Unmarshal(raw, &c); err != nil {
			f.t.Errorf("%s body: %v", method, err)
		}
		c.Method = method
		f.mu.Lock()
		if method == "sendMessage" {
			f.nextMsg++
			c.MessageID = f.nextMsg
		}
		f.calls = append(f.calls, c)
		id := c.MessageID
		f.mu.Unlock()
		if method == "sendMessage" {
			reply(map[string]any{"message_id": id, "chat": map[string]any{"id": c.ChatID, "type": "group"}})
			return
		}
		reply(true)
	case "answerCallbackQuery":
		f.mu.Lock()
		f.answers++
		f.mu.Unlock()
		reply(true)
	default:
		f.t.Errorf("unexpected method %s", method)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeTelegram) push(u map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u["update_id"] = f.nextID
	f.nextID++
	f.queue = append(f.queue, u)
}

func (f *fakeTelegram) text(user, chat int64, msg string) {
	f.push(map[string]any{"message": map[string]any{
		"message_id": 1, "from": map[string]any{"id": user}, "chat": map[string]any{"id": chat, "type": "group"}, "text": msg,
	}})
}

func (f *fakeTelegram) tap(user, chat int64, msgID int, data string) {
	f.push(map[string]any{"callback_query": map[string]any{
		"id": fmt.Sprintf("cb%d", f.nextID), "from": map[string]any{"id": user}, "data": data,
		"message": map[string]any{"message_id": msgID, "chat": map[string]any{"id": chat, "type": "group"}},
	}})
}

// waitFor returns the first recorded call (from index from on) matching pred.
func (f *fakeTelegram) waitFor(from int, pred func(tgCall) bool) (tgCall, int) {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for i := from; i < len(f.calls); i++ {
			if pred(f.calls[i]) {
				c := f.calls[i]
				f.mu.Unlock()
				return c, i + 1
			}
		}
		f.mu.Unlock()
		time.Sleep(30 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t.Fatalf("no matching Telegram call; calls so far: %+v", f.calls)
	return tgCall{}, 0
}

func button(t *testing.T, c tgCall, match func(string) bool) string {
	t.Helper()
	for _, row := range c.Markup.Rows {
		for _, b := range row {
			if match(b.Text) {
				return b.Data
			}
		}
	}
	t.Fatalf("no matching button in %+v", c.Markup.Rows)
	return ""
}

func writeBotEnv(t *testing.T, apiURL string, allow string) string {
	t.Helper()
	path := t.TempDir() + "/bot.env"
	body := "TELEGRAM_BOT_TOKEN=" + fakeBotToken + "\nBRIDGE_BOT_ALLOW=" + allow + "\nBRIDGE_BOT_API_URL=" + apiURL + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// lockedBuffer is a bytes.Buffer safe to read while the child writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startBot(t *testing.T, cfgPath string, env []string) *lockedBuffer {
	t.Helper()
	cmd := bridgeCmd("bot", "--config", cfgPath)
	cmd.Env = env
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("bot stderr:\n%s", stderr.String())
		}
	})
	return stderr
}

func TestBotE2E_PickerDashboardStartSendsRCLink(t *testing.T) {
	bin, tmuxLog := fakeTmux(t)
	tg := newFakeTelegram(t)
	const me, chat = int64(111), int64(-5)
	stderr := startBot(t, writeBotEnv(t, tg.srv.URL, "111"),
		launchEnv(t, bin, "FAKE_TMUX_PANE=Remote Control: https://claude.ai/code/session_01E2E"))

	tg.text(me, chat, "/nav")
	// The picker is sent while repos load, then edited in place once loaded.
	pick, _ := tg.waitFor(0, func(c tgCall) bool {
		return strings.HasPrefix(c.Text, "🗂 bridge · repos") && strings.Contains(c.Text, "3 repos")
	})
	if pick.ChatID != chat || pick.MessageID == 0 {
		t.Errorf("picker = chat %d message %d, want chat %d", pick.ChatID, pick.MessageID, chat)
	}

	tg.tap(me, chat, pick.MessageID, button(t, pick, func(s string) bool { return s == "bridge" }))
	dash, next := tg.waitFor(0, func(c tgCall) bool {
		return c.Method == "editMessageText" && strings.HasPrefix(c.Text, "📁 bridge") && len(c.Markup.Rows) > 0 && strings.HasSuffix(c.Markup.Rows[0][0].Text, " ▶")
	})

	tg.tap(me, chat, dash.MessageID, button(t, dash, func(s string) bool { return strings.HasSuffix(s, " ▶") }))
	link, _ := tg.waitFor(next, func(c tgCall) bool {
		return c.Method == "sendMessage" && strings.Contains(c.Text, "https://claude.ai/code/session_01E2E")
	})
	if !strings.HasPrefix(link.Text, "🔗 bridge") {
		t.Errorf("link message = %q", link.Text)
	}
	log, _ := os.ReadFile(tmuxLog)
	if !strings.Contains(string(log), "new-session -d -s bridge") || !strings.Contains(string(log), "--remote-control") {
		t.Errorf("tmux log lacks a detached RC launch:\n%s", log)
	}
	if strings.Contains(stderr.String(), fakeBotToken) {
		t.Errorf("token logged:\n%s", stderr.String())
	}
}

func TestBotE2E_IgnoresNonAllowlistedUser(t *testing.T) {
	bin, _ := fakeTmux(t)
	tg := newFakeTelegram(t)
	stderr := startBot(t, writeBotEnv(t, tg.srv.URL, "111"), launchEnv(t, bin))

	tg.text(999, -5, "/nav")
	tg.text(111, -5, "/nav")
	tg.waitFor(0, func(c tgCall) bool { return c.Method == "sendMessage" })
	time.Sleep(300 * time.Millisecond)
	tg.mu.Lock()
	defer tg.mu.Unlock()
	sends := 0
	for _, c := range tg.calls {
		if c.Method == "sendMessage" {
			sends++
		}
	}
	if sends != 1 {
		t.Errorf("sendMessage calls = %d, want 1 (only the allowlisted user)", sends)
	}
	if !strings.Contains(stderr.String(), "user=999") {
		t.Errorf("drop not logged:\n%s", stderr.String())
	}
}

// Review Focus: `bridge bot` refuses a world-readable token file.
func TestBotE2E_RefusesWorldReadableConfig(t *testing.T) {
	path := writeBotEnv(t, "http://127.0.0.1:1", "111")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("bot", "--config", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err == nil || !errors.As(err, &ee) || ee.ExitCode() == 0 {
		t.Fatalf("want a non-zero exit, got %v", err)
	}
	if !strings.Contains(stderr.String(), "chmod 600") || strings.Contains(stderr.String(), fakeBotToken) {
		t.Errorf("stderr = %q", stderr.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/bridge/ -run 'TestLoadBotConfig|TestDefaultBotConfigPath|TestBotE2E'`
Expected: FAIL — `undefined: loadBotConfig`, `undefined: botConfig`, `undefined: defaultBotConfigPath`.

- [ ] **Step 3: Extract the issue fetcher nav already uses**

In `cmd/bridge/nav.go` replace the inline `FetchIssues: func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error) { … }` closure in the `nav.Config` literal with `FetchIssues: fetchOpenIssues,` and add, above `navDebugPath`:

```go
// fetchOpenIssues lists a repo's open issues via the per-forge client; a forge
// without a configured client yields none. Shared by nav and `bridge bot`.
func fetchOpenIssues(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error) {
	c := clientFor(forgeName)
	if c == nil {
		return nil, nil
	}
	return c.ListOpenIssues(ctx, owner, repo)
}
```

- [ ] **Step 4: Implement the command, config and runner**

Create `cmd/bridge/bot.go`:

```go
//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/bot"
	"github.com/freaxnx01/bridge/internal/telegram"
)

var botConfigPath string

var botCmd = &cobra.Command{
	Use:   "bot",
	Short: "Run the Telegram nav bot: nav's repo picker and dashboard as inline buttons",
	Long: `bot long-polls a Telegram bot and serves nav's repo picker and per-repo
dashboard as inline-keyboard buttons: open a dashboard, start/open a session
(the Remote Control link arrives as its own message), stop it, create a
worktree, start a session on an issue, clone a remote repo, create a repo.

Config: ~/.config/bridge/bot.env (mode 0600) with TELEGRAM_BOT_TOKEN=… and
BRIDGE_BOT_ALLOW=<telegram user id>[,<id>]. See docs/telegram-bot.md.`,
	Args: cobra.NoArgs,
	RunE: runBot,
}

func init() {
	botCmd.Flags().StringVar(&botConfigPath, "config", "", "bot.env path (default $XDG_CONFIG_HOME/bridge/bot.env, else ~/.config/bridge/bot.env)")
	rootCmd.AddCommand(botCmd)
}

// botConfig is the parsed bot.env.
type botConfig struct {
	Token  string
	Allow  []int64
	APIURL string // optional BRIDGE_BOT_API_URL (tests / proxies)
}

func defaultBotConfigPath(getenv func(string) string) string {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "bridge", "bot.env")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bridge", "bot.env")
}

// loadBotConfig reads KEY=VALUE lines from path. It refuses a missing file and
// one readable by group or others (it holds the bot token). The token is never
// part of an error message.
func loadBotConfig(path string) (botConfig, error) {
	info, err := os.Stat(path)
	if err != nil {
		return botConfig{}, fmt.Errorf("bridge bot: config %s not found — create it (mode 0600) with TELEGRAM_BOT_TOKEN and BRIDGE_BOT_ALLOW; see docs/telegram-bot.md: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return botConfig{}, fmt.Errorf("bridge bot: %s has mode %04o but holds the bot token — run: chmod 600 %s", path, perm, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return botConfig{}, fmt.Errorf("bridge bot: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // read-only
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		vals[strings.TrimSpace(k)] = v
	}
	if err := sc.Err(); err != nil {
		return botConfig{}, fmt.Errorf("bridge bot: read %s: %w", path, err)
	}
	cfg := botConfig{Token: vals["TELEGRAM_BOT_TOKEN"], APIURL: vals["BRIDGE_BOT_API_URL"]}
	if cfg.Token == "" {
		return botConfig{}, fmt.Errorf("bridge bot: TELEGRAM_BOT_TOKEN missing in %s", path)
	}
	for _, part := range strings.Split(vals["BRIDGE_BOT_ALLOW"], ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return botConfig{}, fmt.Errorf("bridge bot: BRIDGE_BOT_ALLOW in %s: %q is not a Telegram user id", path, part)
		}
		cfg.Allow = append(cfg.Allow, id)
	}
	if len(cfg.Allow) == 0 {
		return botConfig{}, fmt.Errorf("bridge bot: BRIDGE_BOT_ALLOW missing in %s (comma-separated Telegram user ids)", path)
	}
	return cfg, nil
}

func runBot(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true
	path := botConfigPath
	if path == "" {
		path = defaultBotConfigPath(os.Getenv)
	}
	cfg, err := loadBotConfig(path)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	var opts []telegram.Option
	if cfg.APIURL != "" {
		opts = append(opts, telegram.WithBaseURL(cfg.APIURL))
	}
	allow := make(map[int64]bool, len(cfg.Allow))
	for _, id := range cfg.Allow {
		allow[id] = true
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r := newBotRunner(telegram.New(cfg.Token, opts...), bot.Config{Allow: allow}, newBotEnv(), log)
	log.Info("bridge bot started", "allowlisted", len(allow))
	if err := r.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("bridge bot stopped")
	return nil
}

// chatEvent is an event for one chat; callbackID is set for taps.
type chatEvent struct {
	chatID     int64
	ev         bot.Event
	callbackID string
}

// chatSlot is the runner's per-chat bookkeeping around bot.State.
type chatSlot struct {
	state    bot.State
	msgID    int
	lastText string
	lastKB   string
}

// botRunner owns all chat state. Only run's goroutine touches chats;
// pollers and actions talk to it through events.
type botRunner struct {
	tg          *telegram.Client
	cfg         bot.Config
	env         botEnv
	log         *slog.Logger
	events      chan chatEvent
	chats       map[int64]*chatSlot
	pollTimeout time.Duration
}

func newBotRunner(tg *telegram.Client, cfg bot.Config, env botEnv, log *slog.Logger) *botRunner {
	return &botRunner{
		tg: tg, cfg: cfg, env: env, log: log,
		events:      make(chan chatEvent, 64),
		chats:       map[int64]*chatSlot{},
		pollTimeout: 50 * time.Second,
	}
}

// run serves until ctx ends.
func (r *botRunner) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ce := <-r.events:
			r.handle(ctx, ce)
		}
	}
}

// poll long-polls getUpdates forever. The client already retries 429/5xx;
// anything it gives up on (409 included) is logged and backed off here.
func (r *botRunner) poll(ctx context.Context) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		pctx, cancel := context.WithTimeout(ctx, r.pollTimeout+15*time.Second)
		ups, err := r.tg.GetUpdates(pctx, offset, r.pollTimeout)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, telegram.ErrConflict) {
				r.log.Warn("another process polls this token; backing off", "wait", backoff)
			} else {
				r.log.Warn("getUpdates failed", "err", err, "wait", backoff)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 60*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			offset = u.UpdateID + 1
			ce, ok := toChatEvent(u)
			if !ok {
				continue
			}
			select {
			case r.events <- ce:
			case <-ctx.Done():
				return
			}
		}
	}
}

func toChatEvent(u telegram.Update) (chatEvent, bool) {
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil:
		q := u.CallbackQuery
		return chatEvent{chatID: q.Message.Chat.ID, callbackID: q.ID, ev: bot.Tap{
			UserID: q.From.ID, ChatID: q.Message.Chat.ID, MessageID: q.Message.MessageID, Data: q.Data,
		}}, true
	case u.Message != nil && u.Message.From != nil && u.Message.Text != "":
		m := u.Message
		return chatEvent{chatID: m.Chat.ID, ev: bot.Text{UserID: m.From.ID, ChatID: m.Chat.ID, Text: m.Text}}, true
	}
	return chatEvent{}, false
}

// handle runs one event through bot.Update, executes the actions and
// re-renders the chat's message.
func (r *botRunner) handle(ctx context.Context, ce chatEvent) {
	slot := r.chats[ce.chatID]
	if slot == nil {
		slot = &chatSlot{}
	}
	next, actions := bot.Update(r.cfg, slot.state, ce.ev)
	for _, a := range actions {
		if d, ok := a.(bot.Drop); ok {
			r.log.Info("dropped update from a user not in BRIDGE_BOT_ALLOW", "user", d.UserID, "chat", d.ChatID)
			return // no state, no answer, no reply
		}
	}
	r.chats[ce.chatID] = slot
	slot.state = next
	toast, newMsg := "", false
	for _, a := range actions {
		switch a := a.(type) {
		case bot.Toast:
			toast = a.Text
		case bot.NewMessage:
			newMsg = true
		case bot.SendLink:
			r.send(ctx, ce.chatID, a.Text)
		default:
			r.dispatch(ctx, ce.chatID, a)
		}
	}
	if ce.callbackID != "" {
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := r.tg.AnswerCallbackQuery(actx, ce.callbackID, toast); err != nil {
			r.log.Warn("answerCallbackQuery failed", "err", err)
		}
		cancel()
	}
	if newMsg {
		r.sendScreen(ctx, ce.chatID, slot)
	} else if slot.msgID != 0 {
		r.editScreen(ctx, ce.chatID, slot)
	}
}

// dispatch executes a slow action off the event loop under its timeout and
// feeds the result back as an event — always, so Busy always clears.
func (r *botRunner) dispatch(ctx context.Context, chatID int64, a bot.Action) {
	timeout := actionTimeout(a)
	go func() {
		actx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ev := r.env.exec(actx, a)
		if ev == nil {
			return
		}
		select {
		case r.events <- chatEvent{chatID: chatID, ev: ev}:
		case <-ctx.Done():
		}
	}()
}

// actionTimeout: 2 min for clone, fetch, repo creation and a forge refresh; 30 s otherwise.
func actionTimeout(a bot.Action) time.Duration {
	switch a := a.(type) {
	case bot.Clone, bot.CreateRepo, bot.Fetch:
		return 2 * time.Minute
	case bot.LoadRepos:
		if a.Refresh {
			return 2 * time.Minute
		}
	}
	return 30 * time.Second
}

func toMarkup(kb bot.Keyboard) *telegram.InlineKeyboardMarkup {
	m := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{}}
	for _, row := range kb {
		out := make([]telegram.InlineKeyboardButton, 0, len(row))
		for _, b := range row {
			out = append(out, telegram.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data})
		}
		m.InlineKeyboard = append(m.InlineKeyboard, out)
	}
	return m
}

func kbKey(kb bot.Keyboard) string {
	b, _ := json.Marshal(kb) // plain structs; cannot fail
	return string(b)
}

func (r *botRunner) send(ctx context.Context, chatID int64, text string) {
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := r.tg.SendMessage(sctx, chatID, text, nil); err != nil {
		r.log.Warn("sendMessage failed", "err", err)
	}
}

func (r *botRunner) sendScreen(ctx context.Context, chatID int64, slot *chatSlot) {
	text, kb := bot.Render(slot.state)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m, err := r.tg.SendMessage(sctx, chatID, text, toMarkup(kb))
	if err != nil {
		r.log.Warn("sendMessage failed", "err", err)
		return
	}
	slot.msgID = m.MessageID
	slot.state, _ = bot.Update(r.cfg, slot.state, bot.MessageSent{MessageID: m.MessageID})
	slot.lastText, slot.lastKB = text, kbKey(kb)
}

func (r *botRunner) editScreen(ctx context.Context, chatID int64, slot *chatSlot) {
	text, kb := bot.Render(slot.state)
	key := kbKey(kb)
	if text == slot.lastText && key == slot.lastKB {
		return
	}
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := r.tg.EditMessageText(ectx, chatID, slot.msgID, text, toMarkup(kb))
	if err != nil && !errors.Is(err, telegram.ErrNotModified) {
		r.log.Warn("editMessageText failed", "err", err)
		return
	}
	slot.lastText, slot.lastKB = text, key
}
```

Create `cmd/bridge/bot_env.go`:

```go
//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freaxnx01/bridge/internal/bot"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/launch"
	"github.com/freaxnx01/bridge/internal/navdata"
	"github.com/freaxnx01/bridge/internal/remote"
)

// rcWait is how long a bot start waits for claude's Remote Control link
// (inside the 30 s action timeout).
const rcWait = 20 * time.Second

// botEnv is the bot's I/O: each field performs one bot.Action.
type botEnv struct {
	loadRepos  func(ctx context.Context, refresh bool) (local, remote []navdata.RepoEntry, mru []string, err error)
	dashRows   func(ctx context.Context, repo core.Repo) []navdata.DashRow
	status     func(ctx context.Context, path string) (navdata.Dirty, error)
	fetch      func(ctx context.Context, repo core.Repo) error
	issues     func(ctx context.Context, repo core.Repo) []forge.Issue
	start      func(ctx context.Context, a bot.Start) bot.Started
	stop       func(ctx context.Context, slot string) error
	clone      navdata.CloneFunc
	createRepo navdata.CreateRepoFunc
}

// newBotEnv wires the bot to the same data and launch paths nav and
// `bridge launch` use.
func newBotEnv() botEnv {
	cache := cacheRoot()
	remotePath := filepath.Join(cache, "remote.list")
	return botEnv{
		loadRepos: func(ctx context.Context, refresh bool) ([]navdata.RepoEntry, []navdata.RepoEntry, []string, error) {
			roots := reposRoots()
			var rem []navdata.RepoEntry
			var err error
			if refresh {
				var refs []forge.RepoRef
				refs, err = remote.Refresh(ctx, roots, remotePath)
				rem = navdata.RemoteEntries(refs)
			}
			if !refresh || (err != nil && len(rem) == 0) {
				// No refresh, or a total refresh failure: show the cache.
				cached, cerr := navdata.RemoteRepos(remotePath)
				rem, err = cached, errors.Join(err, cerr)
			}
			return navdata.LocalRepos(ctx, roots), rem, navdata.RecentPaths(filepath.Join(cache, "mru")), err
		},
		dashRows: func(ctx context.Context, repo core.Repo) []navdata.DashRow {
			return navdata.DashRows(ctx, loadSessions, repo, filepath.Join(cache, "slots.json"))
		},
		status: navdata.GitDirty,
		fetch: func(ctx context.Context, repo core.Repo) error {
			return navdata.GitFetch(ctx, repo.Path, repo.Forge)
		},
		issues: func(ctx context.Context, repo core.Repo) []forge.Issue {
			src := navdata.IssueSource{CacheDir: filepath.Join(cache, "issues"), Fetch: fetchOpenIssues}
			return navdata.IssuesCached(ctx, src, repo.Forge, repo.Owner, repo.Name)
		},
		start: startForBot,
		stop: func(ctx context.Context, slot string) error {
			return launch.Stop(ctx, launchDeps(), slot)
		},
		clone: func(_ context.Context, ref forge.RepoRef) (core.Repo, error) {
			dir, err := cloneRemoteRepo(ref)
			if err != nil {
				return core.Repo{}, err
			}
			return repoFromClonedRef(reposRoot(), ref, dir), nil
		},
		createRepo: func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error) {
			repo, _, err := createAndClone(ctx, name, forgeName, private)
			return repo, err
		},
	}
}

// startForBot creates the worktree when asked, then starts (or finds) the
// session detached with Remote Control and waits for its link. The agent is
// BRIDGE_DEFAULT_AGENT (+ _ARGS), falling back to claude like nav; Remote
// Control is only requested for claude. No pre-launch sync (nav parity).
func startForBot(ctx context.Context, a bot.Start) bot.Started {
	d := launchDeps()
	if a.Create {
		if _, err := navdata.CreateWorktree(ctx, a.Repo, a.Worktree, a.Label); err != nil {
			return bot.Started{Err: errors.New(navdata.WorktreeErrMessage(err))}
		}
	}
	agent := ""
	if os.Getenv("BRIDGE_DEFAULT_AGENT") == "" {
		agent = "claude"
	}
	t, err := launch.TargetFor(ctx, d, a.Repo, a.Worktree, agent)
	if err != nil {
		return bot.Started{Err: err}
	}
	t.Label = a.Label
	rc := t.Spec.Name == "claude"
	running, err := launch.StartDetached(ctx, d, &t, rc, true)
	if err != nil {
		return bot.Started{Slot: t.Slot, Err: err}
	}
	res := bot.Started{Slot: t.Slot, AlreadyRunning: running}
	if rc {
		res.RCURL = launch.WaitRCURL(ctx, d, t.Slot, rcWait)
		res.Exited = res.RCURL == "" && !launch.SessionLive(d, t.Slot)
	}
	return res
}

// within runs fn but gives up when ctx ends, so a call that ignores ctx
// (cloneRemoteRepo) can never keep an action — and the chat's Busy — open
// past its timeout. The abandoned call finishes in the background.
func within[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("timed out: %w", ctx.Err())
	}
}

// exec performs one action and returns its result event (nil for none).
func (e botEnv) exec(ctx context.Context, a bot.Action) bot.Event {
	switch a := a.(type) {
	case bot.LoadRepos:
		type repos struct {
			local, remote []navdata.RepoEntry
			mru           []string
		}
		r, err := within(ctx, func() (repos, error) {
			l, rm, m, lerr := e.loadRepos(ctx, a.Refresh)
			return repos{l, rm, m}, lerr
		})
		return bot.ReposLoaded{Local: r.local, Remote: r.remote, MRU: r.mru, Err: err}
	case bot.LoadDash:
		rows, _ := within(ctx, func() ([]navdata.DashRow, error) { return e.dashRows(ctx, a.Repo), nil })
		if rows == nil {
			rows = []navdata.DashRow{}
		}
		return bot.DashLoaded{Repo: a.Repo, Rows: rows}
	case bot.LoadStatus:
		st := make(map[string]bot.RowStatus, len(a.Paths))
		for _, p := range a.Paths {
			d, err := within(ctx, func() (navdata.Dirty, error) { return e.status(ctx, p) })
			st[p] = bot.RowStatus{Dirty: d, Err: err != nil}
		}
		return bot.StatusLoaded{Repo: a.Repo, Status: st}
	case bot.Fetch:
		_, err := within(ctx, func() (struct{}, error) { return struct{}{}, e.fetch(ctx, a.Repo) })
		return bot.FetchDone{Repo: a.Repo, Err: err}
	case bot.LoadIssues:
		is, _ := within(ctx, func() ([]forge.Issue, error) { return e.issues(ctx, a.Repo), nil })
		return bot.IssuesLoaded{Repo: a.Repo, Issues: is}
	case bot.Start:
		res, err := within(ctx, func() (bot.Started, error) { return e.start(ctx, a), nil })
		if err != nil {
			return bot.Started{Err: err}
		}
		return res
	case bot.Stop:
		_, err := within(ctx, func() (struct{}, error) { return struct{}{}, e.stop(ctx, a.Slot) })
		return bot.Stopped{Slot: a.Slot, Err: err}
	case bot.Clone:
		repo, err := within(ctx, func() (core.Repo, error) { return e.clone(ctx, a.Ref) })
		return bot.Cloned{Repo: repo, Err: err}
	case bot.CreateRepo:
		repo, err := within(ctx, func() (core.Repo, error) { return e.createRepo(ctx, a.Name, a.Forge, a.Private) })
		return bot.RepoCreated{Repo: repo, Err: err}
	}
	return nil
}
```

Create `cmd/bridge/bot_windows.go`:

```go
//go:build windows

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// botCmd is a hidden stub: it keeps rewritePositional from turning
// `bridge bot` into `open bot` on Windows.
var botCmd = &cobra.Command{
	Use:                "bot",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE: func(*cobra.Command, []string) error {
		return errors.New("bridge bot: unix-only (needs tmux)")
	},
}

func init() { rootCmd.AddCommand(botCmd) }
```

- [ ] **Step 5: Run the new tests, then the whole suite**

Run: `go test -race ./cmd/bridge/ -run 'TestLoadBotConfig|TestDefaultBotConfigPath|TestBotE2E' -v`
Expected: `--- PASS` for `TestLoadBotConfig_Parses`, `TestLoadBotConfig_RefusesGroupOrWorldReadable`, `TestLoadBotConfig_Errors`, `TestDefaultBotConfigPath`, `TestBotE2E_PickerDashboardStartSendsRCLink`, `TestBotE2E_IgnoresNonAllowlistedUser`, `TestBotE2E_RefusesWorldReadableConfig`; `ok  	github.com/freaxnx01/bridge/cmd/bridge`.

Run: `gofmt -l . && go vet ./... && go test -race ./... && GOOS=windows go build ./...`
Expected: no gofmt output, every package `ok`, Windows build silent.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/bot.go cmd/bridge/bot_env.go cmd/bridge/bot_windows.go cmd/bridge/bot_config_test.go cmd/bridge/bot_test.go cmd/bridge/nav.go
git commit -m "feat(bot): bridge bot command — Telegram buttons for nav's picker and dashboard"
```

---

### Task 8: Operations, docs, and removal of the Python bot

**Files:**
- Create: `docs/systemd/bridge-bot.service`, `docs/telegram-bot.md`
- Modify: `justfile` (new `bot-install` recipe), `README.md`, `docs/ideas.md`
- Delete: `bridge-bot/` (whole directory, incl. `bridge-bot/systemd/bridge-bot.service` and `bridge-bot/README.md`, the only in-tree reference to `setup-claude-channels.sh`, which itself no longer exists in the repo)

**Interfaces:**
- Consumes (Task 7): the `bridge bot` command, `~/.config/bridge/bot.env` keys `TELEGRAM_BOT_TOKEN`, `BRIDGE_BOT_ALLOW`.
- Produces: `just bot-install`; systemd user unit `bridge-bot.service` (`Restart=always`, `RestartSec=10`, `ExecStart=%h/.local/bin/bridge bot`).

- [ ] **Step 1: Write the check that fails today**

Run: `test -f docs/systemd/bridge-bot.service && test -f docs/telegram-bot.md && just --list | grep -q bot-install && ! test -e bridge-bot && echo READY`
Expected: no `READY` (the files do not exist yet and `bridge-bot/` is still there).

- [ ] **Step 2: Add the unit, the recipe and the setup doc**

Create `docs/systemd/bridge-bot.service`:

```ini
# Telegram nav bot (`bridge bot`): nav's repo picker + dashboard as inline
# buttons. Install: `just bot-install`. Logs: journalctl --user -u bridge-bot
# The token is read from ~/.config/bridge/bot.env (mode 0600) by the binary
# itself — never via EnvironmentFile, so it is not inherited by launched sessions.
[Unit]
Description=bridge Telegram nav bot (bridge bot)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
ExecStart=%h/.local/bin/bridge bot
Restart=always
RestartSec=10
WorkingDirectory=%h

[Install]
WantedBy=default.target
```

In `justfile`, insert directly after the `admin-install` recipe (before `# Run Go + shim tests.`):

```just
# Needs ~/.config/bridge/bot.env (docs/telegram-bot.md); run from the main checkout.
# Install + enable the Telegram nav bot (`bridge bot`) as a systemd user service.
[unix]
bot-install:
    test -f "$HOME/.config/bridge/bot.env" || { echo "missing ~/.config/bridge/bot.env — see docs/telegram-bot.md" >&2; exit 1; }
    mkdir -p "$HOME/.config/systemd/user"
    ln -sfn "{{justfile_directory()}}/docs/systemd/bridge-bot.service" "$HOME/.config/systemd/user/bridge-bot.service"
    systemctl --user daemon-reload
    systemctl --user enable --now bridge-bot.service
    systemctl --user --no-pager status bridge-bot.service | head -5
```

Create `docs/telegram-bot.md`:

````markdown
# Telegram nav bot (`bridge bot`)

`bridge bot` puts `bridge nav`'s repo picker and per-repo dashboard on your
phone as Telegram inline-keyboard buttons. Taps are deterministic — no Claude
in the loop, no tokens spent:

- **Repos** (`/nav` or `/start`): MRU repos first (⭐), then A–Z; remote-only
  repos are marked ↓ and cloned on tap (with a confirm). Forge filter
  (`All ▸`), text filter (🔍, then send the text), ➕ new repo, 🔄 refresh.
- **Dashboard**: one line per worktree with session state (● running, ○ none)
  and git status (✓ clean, ✎N modified, ↑ahead ↓behind, ∅ no upstream, … loading).
  `▶` starts or opens a session — the Remote Control link arrives as its own
  message — `⏹` stops one (with a confirm), ➕ creates a worktree and starts a
  session in it, 🐞 lists open issues and starts a session on one
  (`issue` worktree `<n>-<slug>`, session `#<n> [title]`, exactly like nav).

This is a different bot from the Claude admin session (`@agent_dev_ctl_bot`,
[admin-session.md](admin-session.md)): a Bot API token can be long-polled by
one process only, so each needs its own bot.

## 1. Create the bot

1. In Telegram, talk to **@BotFather** → `/newbot`. Give it a name with
   "bridge" in it (e.g. `freax_bridge_bot`). Copy the token.
2. Store the token in Passbolt (entry e.g. `Telegram bridge nav bot`).
3. Find your numeric Telegram user id (e.g. send a message to **@userinfobot**).

## 2. Write `~/.config/bridge/bot.env`

```sh
mkdir -p ~/.config/bridge
install -m 600 /dev/null ~/.config/bridge/bot.env
$EDITOR ~/.config/bridge/bot.env
```

```sh
TELEGRAM_BOT_TOKEN=123456789:AA…
BRIDGE_BOT_ALLOW=111111111          # comma-separated user ids; everyone else is ignored
```

`bridge bot` refuses to start when the file is missing or readable by group or
others (`chmod 600` fixes it). The token is never logged and never passed on a
command line.

Sessions start with `BRIDGE_DEFAULT_AGENT` / `BRIDGE_DEFAULT_AGENT_ARGS`
(claude when unset), like nav. To set them for the service:
`systemctl --user edit bridge-bot` and add e.g.
`Environment=BRIDGE_DEFAULT_AGENT_ARGS=--dangerously-skip-permissions`.

## 3. Install the service

From the main checkout (the unit is symlinked from the repo):

```sh
just build        # installs ~/.local/bin/bridge
just bot-install  # links + enables + starts bridge-bot.service
journalctl --user -u bridge-bot -f
```

Send `/nav` to the bot. A `getUpdates failed … 409` / "another process polls
this token" warning means a second process (an old bot?) uses the same token.

## Security

- Only allowlisted user ids get answers; other members of a group are ignored
  (one log line each).
- Free text is only ever a filter or a new repo/worktree name
  (`^[A-Za-z0-9._-]{1,40}$`); nothing from Telegram reaches a shell.
- Buttons carry short ids (`7:r:12`) resolved against the chat's state, never
  paths. Buttons of an older message answer "⚠ outdated — /nav".
- Stop, clone and new repo always ask for confirmation. There are no delete actions.
````

- [ ] **Step 3: Update README and ideas, remove the Python bot**

In `README.md`, four edits:

1. In the `## CLI surface` code block, add after the `bridge nav …` line:

```text
bridge bot                      # Telegram nav bot: nav's picker + dashboard as phone buttons (unix only; docs/telegram-bot.md)
```

2. Add after the line starting `Telegram admin session (steer bridge from your phone)`:

```markdown
Telegram nav bot (nav's buttons on your phone, no Claude in the loop): see [docs/telegram-bot.md](docs/telegram-bot.md).
```

3. In the `## Go packages` table, add after the `internal/agents` row:

```markdown
| `internal/navdata` | nav's data loaders + mutations as plain functions, shared by `bridge nav` and `bridge bot` |
| `internal/launch` | repo/worktree/agent resolution + detached tmux start, shared by `bridge <repo>`, `bridge launch`, `bridge bot` |
| `internal/telegram` | minimal Telegram Bot API client (stdlib) |
| `internal/bot` | `bridge bot`'s pure screen state machine + rendering |
```

4. In `## Carryovers from bash bridge`, replace the bullet starting `- **Presence-aware Telegram pages**` with the bullet below, and delete the paragraph that starts with `` `bridge-bot/` (the Python Telegram spawner) `` together with its preceding blank line:

```markdown
- **Presence-aware Telegram pages** (idle / elicitation / usage-limit notifications). Replaced operationally by Remote Control for live-session interaction; phone-side navigation is `bridge bot` ([docs/telegram-bot.md](docs/telegram-bot.md)).
```

Append to `docs/ideas.md` (out-of-scope items of the spec):

```markdown
## bridge bot: Overview and Agents screens
- id: 2026-10-26-bot-overview-agents · captured: 2026-10-26 · status: raw
- value: Phone access to nav's cross-repo Overview (ranked issues/roadmap/captures) and the Agents screen
Out of v1 of the Telegram nav bot (spec docs/superpowers/specs/2026-10-11-bridge-telegram-nav-bot-design.md). Would add screens to internal/bot reusing overview.Build and the agentview data nav already loads.

## bridge bot: idea / issue capture
- id: 2026-10-26-bot-capture · captured: 2026-10-26 · status: raw
- value: Capture an idea or open an issue from the phone without a Claude session
Out of v1 of the Telegram nav bot. Free text would go to the existing capture paths (internal/capture); needs a prompt flow and repo/alias choice.

## bridge bot: notes preview (ideas.md / TODO.md)
- id: 2026-10-26-bot-notes-preview · captured: 2026-10-26 · status: raw
- value: Read a repo's backlog files on the dashboard, like nav's right column
Out of v1 of the Telegram nav bot. nav's readRepoNotes would move to internal/navdata first.

## bridge bot: git side panels (branches / recent commits / status)
- id: 2026-10-26-bot-git-panels · captured: 2026-10-26 · status: raw
- value: nav's per-worktree detail panels on the phone
Out of v1 of the Telegram nav bot. nav's gitBranchesCmd/gitCommitsCmd/gitStatusCmd parsers would move to internal/navdata.

## bridge bot: multiple concurrent users
- id: 2026-10-26-bot-multi-user · captured: 2026-10-26 · status: raw
- value: More than one person steering the same bridge host
v1 keeps per-chat state and an allowlist but assumes one operator: Busy and confirms are per chat, not per user, and there is no per-user audit. Out of v1 of the Telegram nav bot.

## bridge bot: persist chat state across restarts
- id: 2026-10-26-bot-persist-state · captured: 2026-10-26 · status: raw
- value: Buttons keep working after a service restart instead of answering "outdated — /nav"
Out of v1 of the Telegram nav bot. Would store per-chat State (message id, epoch, screen, repo) under the cache dir and reload it on start.
```

Remove the Python bot:

```bash
git rm -r -q bridge-bot
grep -rn "bridge-bot/" README.md docs/*.md justfile Makefile || echo "no live references"
```

Expected: `no live references` (history under `docs/plans/`, `docs/specs/`, `docs/superpowers/` and `CHANGELOG.md` stays as written).

- [ ] **Step 4: Verify**

Run: `test -f docs/systemd/bridge-bot.service && test -f docs/telegram-bot.md && just --list | grep -q bot-install && ! test -e bridge-bot && echo READY`
Expected: `READY`

Run: `just --list | grep bot-install && systemd-analyze --user verify docs/systemd/bridge-bot.service; go build ./... && go test ./...`
Expected: the `bot-install` line with the comment "Install + enable the Telegram nav bot (`bridge bot`) as a systemd user service."; `systemd-analyze` prints nothing (or only a note that `%h/.local/bin/bridge` is missing when bridge is not installed); all Go packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add docs/systemd/bridge-bot.service docs/telegram-bot.md justfile README.md docs/ideas.md
git commit -m "docs(bot): systemd unit, just bot-install, setup guide; remove Python bridge-bot"
```

- [ ] **Step 6 (manual, after merge — needs the user): live check**

Not automatable (needs a real bot token and a phone). After merging and `just build` on agent-dev, the user creates the bot with @BotFather, writes `~/.config/bridge/bot.env`, runs `just bot-install`, sends `/nav`, opens a repo, taps ▶ and checks the Remote Control link arrives as its own message, taps ⏹ → ✅, creates a worktree, starts a session on an issue, and checks `journalctl --user -u bridge-bot` shows no token.

---

## Self-Review

**1. Spec coverage**

| Spec requirement | Task |
|---|---|
| navdata: local repos + MRU, cached remote, dashboard rows (base row, live session, dirty/ahead/behind), git fetch, cached issues | 1 |
| navdata: create worktree, issue worktree name/label, clone remote, create repo (clone/create stay in cmd, injected as `navdata.CloneFunc`/`CreateRepoFunc`) | 2, 7 |
| nav keeps behaviour; its `tea.Cmd`s wrap navdata; nav tests unchanged | 1, 2 |
| launch: `ResolveTarget`, `Finalize`, `StartDetached`, `WaitRCURL`, typed lookup errors; cmd keeps flags/exit codes/printing; existing tests green | 3 |
| telegram: GetUpdates (long poll, offset), SendMessage, EditMessageText, AnswerCallbackQuery, keyboards, 429/5xx backoff cap 60 s, typed 409, "not modified" | 4 |
| bot: per-chat state, Update, picker paging (8/page, 2/row), MRU ⭐, ↓ remote, forge filter cycle, text filter + ✖, new repo (name → forge×visibility → confirm), clone confirm, 🔄 | 5, 6 |
| Dashboard lines (● state / ○, ✓ ✎N ↑↓ ∅), ▶ start/open with RC link as separate message + "no link yet"/"exited" wording, ⏹ confirm, ➕ worktree, 🐞 issues paged + confirm, 🔄 reload + fetch, ◀ Repos keeps page/filter | 5, 6, 7 |
| Callback data ≤ 64 bytes, short ids, outdated → "⚠ outdated — /nav" | 5, 6 |
| Allowlist; names `^[A-Za-z0-9._-]{1,40}$`; nothing from Telegram reaches a shell; confirm for stop/clone/new repo; no delete | 5, 7 |
| `bot.env` 0600, refuse missing/too-open, token never logged / never in argv; `BRIDGE_DEFAULT_AGENT(_ARGS)` | 7 |
| Runner: callback answered right after the pure Update, actions in goroutines with 30 s / 2 min timeouts, results fed back, render + edit, "not modified" ignored, 409 back-off | 7 |
| systemd user unit (`Restart=always`, `RestartSec=10`, `ExecStart=%h/.local/bin/bridge bot`), `just bot-install`, `docs/telegram-bot.md`, README line, ideas entries, remove `bridge-bot/` + references | 8 |
| E2E: `bridge bot` vs fake Telegram + fake tmux; live check on agent-dev | 7, 8 (Step 6, manual) |

No gaps found.

**2. Placeholder scan** — searched for "TBD", "TODO", "implement later", "similar to Task", "add error handling": none. Every code step carries the full code; the only manual step (Task 8 Step 6) is the live check the spec itself lists as manual.

**3. Type consistency** — checked: `navdata.DashRow`/`Dirty`/`RepoEntry` field names are identical in Tasks 1, 2, 5, 6, 7; `bot.Started{Slot, AlreadyRunning, Exited, RCURL, Err}` is produced by `startForBot` (Task 7) and consumed by `onStarted` (Task 5); `launch.TargetFor(ctx, d, repo, worktree, agentName)` and `launch.StartDetached(ctx, d, &t, rc, noSync)` match between Tasks 3 and 7; `telegram.InlineKeyboardMarkup` is built from `bot.Keyboard` by `toMarkup` (Task 7); nav's `issueSlugMax`, `repoForgeChoices`, `rowParts`, `matchesAllTerms`, `filterRepos`, `matchesForge`, `sortRepoRows`, `buildDashRows`, `parseDirtyStatus`, `buildSessionRows`, `humanLastAccessed`, `lastAccessedFor`, `buildFetchCmd`, `loadRepoIssuesCmd`, `issueWorktreeName`, `worktreeErrMessage` keep the names and signatures nav's tests use.

**4. Review Focus** — the five failure modes above each have a pinned test in their owning task (2/5/6, 5/7, 7, 5/7, 3/5/6); zero worktrees, >64-byte callback data and token-in-error-string are pinned too.
