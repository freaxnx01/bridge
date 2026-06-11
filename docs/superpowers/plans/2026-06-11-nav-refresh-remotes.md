# Nav Refresh Remotes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `bridge nav` picker `r` key re-query the forge (refreshing the on-disk cache) so newly-created remote repos appear without leaving nav.

**Architecture:** Extract the forge-fetch orchestration from `cmd/bridge/list.go` into a new `internal/remote` package exposing `Refresh(ctx, roots, cachePath)`. `cmd/bridge/list.go` delegates to it (keeping its 1h TTL gate). `internal/nav` reaches it through a new `Config.FetchRemote` DI callback (mirroring the existing `Clone` / `FetchIssues` callbacks), wired in `cmd/bridge/nav.go`. The picker `r` handler runs the callback and reuses the already-wired `remoteMsg` / `remoteErrMsg` / spinner / status plumbing.

**Tech Stack:** Go (stdlib `testing`, table-driven, hand-rolled fakes), Cobra, Bubble Tea. Spec: `docs/superpowers/specs/2026-06-11-nav-refresh-remotes-design.md`.

---

## File Structure

- **Create** `internal/remote/remote.go` — forge-target discovery, token resolution, per-forge fetch, cache write. Public `Refresh`.
- **Create** `internal/remote/remote_test.go` — table tests for `discoverRemoteTargets` + a no-token `Refresh` cache-write test.
- **Modify** `cmd/bridge/list.go` — delete the moved funcs; `loadOrFetchRemote` delegates to `remote.Refresh`. Keep `dirExists`/`fileExists` (still used by `bases.go`) and `remoteTTL`.
- **Modify** `internal/nav/types.go` — add `FetchRemote` field to `Config`.
- **Modify** `internal/nav/data.go` — add `(Model) refreshRemoteCmd()`.
- **Modify** `internal/nav/update.go:307-309` — `r` handler calls `refreshRemoteCmd`.
- **Modify** `internal/nav/update_test.go` — `r`-key behavior tests.
- **Modify** `cmd/bridge/nav.go` — wire `FetchRemote` to `remote.Refresh`.
- **Modify** `internal/nav/view.go:138` — add `r refresh` to the picker hint line.

---

## Task 1: Extract `internal/remote` and delegate from `bridge list`

Extract the fetch orchestration into a new package and repoint `list.go` at it in one cohesive change, so the tree compiles at the commit and no logic is duplicated.

**Files:**
- Create: `internal/remote/remote.go`
- Create: `internal/remote/remote_test.go`
- Modify: `cmd/bridge/list.go` (delete moved funcs lines 92-153, 228-295; rewrite `loadOrFetchRemote` lines 192-226)

- [ ] **Step 1: Write the failing test for the new package**

Create `internal/remote/remote_test.go`:

```go
package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestDiscoverRemoteTargets_LayoutVariants(t *testing.T) {
	root := t.TempDir()
	// github owner with .envrc at owner level
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	// github owner with .envrc only under a visibility subdir
	mustMkdirEnvrc(t, filepath.Join(root, "github", "globex", "public"))
	// gitlab owner with .envrc at owner level
	mustMkdirEnvrc(t, filepath.Join(root, "gitlab", "initech"))
	// forgejo + ado markers at fixed locations
	mustMkdirEnvrc(t, filepath.Join(root, "git-forgejo"))
	mustMkdirEnvrc(t, filepath.Join(root, "ado"))
	// github owner WITHOUT any .envrc -> no target
	if err := os.MkdirAll(filepath.Join(root, "github", "noenv"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverRemoteTargets(root)

	want := map[string]string{ // forge|owner -> present
		"github|acme":    "",
		"github|globex":  "",
		"gitlab|initech": "",
		"forgejo|freax":  "",
		"ado|":           "",
	}
	if len(got) != len(want) {
		t.Fatalf("discoverRemoteTargets returned %d targets, want %d: %+v", len(got), len(want), got)
	}
	for _, tgt := range got {
		key := tgt.Forge + "|" + tgt.Owner
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected target %q (%+v)", key, tgt)
		}
	}
}

func TestRefresh_NoToken_WritesCacheNoNetwork(t *testing.T) {
	root := t.TempDir()
	// A github owner marker but no GH_TOKEN in scope -> fetchTargetRepos returns
	// (nil, nil), so Refresh writes an empty cache without any network call.
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")

	repos, err := Refresh(context.Background(), []string{root}, cachePath)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("repos = %d, want 0 (no token)", len(repos))
	}
	if _, err := forge.ReadRepoCache(cachePath); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func mustMkdirEnvrc(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/remote/ -run TestDiscoverRemoteTargets_LayoutVariants -v`
Expected: FAIL — build error, package `internal/remote` has no Go source files / undefined `discoverRemoteTargets`.

- [ ] **Step 3: Create the package by moving the orchestration code**

Create `internal/remote/remote.go` (moved verbatim from `cmd/bridge/list.go`, with `Refresh` as the new public entry point):

```go
// Package remote discovers per-owner forge token scopes and fetches the
// owned repositories across every configured forge, caching the result.
package remote

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// remoteTarget is a (forge, owner) tuple discovered via an .envrc file under a
// repos root. Each target is queried independently with credentials loaded by
// `direnv exec` from that target's Dir.
type remoteTarget struct {
	Dir   string
	Forge string
	Owner string
}

// Refresh discovers every forge target reachable from roots, fetches each
// owner's repos, writes the merged result to cachePath, and returns it. The
// first per-target error is returned alongside whatever repos did succeed, so a
// single failing forge does not lose the others.
func Refresh(ctx context.Context, roots []string, cachePath string) ([]forge.RepoRef, error) {
	var targets []remoteTarget
	seen := map[string]bool{}
	for _, root := range roots {
		for _, t := range discoverRemoteTargets(root) {
			key := t.Forge + "|" + t.Owner + "|" + t.Dir
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, t)
		}
	}
	var all []forge.RepoRef
	var firstErr error
	for _, t := range targets {
		repos, err := fetchTargetRepos(ctx, t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		all = append(all, repos...)
	}
	// Best-effort cache write: callers already have the fresh repos in `all`;
	// a write failure must not fail the refresh.
	_ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
	return all, firstErr
}

// discoverRemoteTargets walks the well-known repos-root layout patterns and
// emits one target per .envrc-marked directory:
//
//	github/<owner>/[<public|private>/].envrc → {github, owner}
//	gitlab/<owner>/.envrc                     → {gitlab, owner}
//	git-forgejo/.envrc                        → {forgejo, freax}
//	ado/.envrc                                → {ado, ""}
//
// GitHub is the only forge that nests an extra visibility level, so its token
// .envrc may live at github/<owner>/<visibility>/.envrc; we accept either
// placement and emit a single deduped target per owner.
func discoverRemoteTargets(root string) []remoteTarget {
	var out []remoteTarget
	if d := filepath.Join(root, "github"); dirExists(d) {
		owners, _ := os.ReadDir(d)
		for _, o := range owners {
			if !o.IsDir() {
				continue
			}
			ownerDir := filepath.Join(d, o.Name())
			markerDir := ownerEnvrcDir(ownerDir)
			if markerDir != "" {
				out = append(out, remoteTarget{Dir: markerDir, Forge: "github", Owner: o.Name()})
			}
		}
	}
	if d := filepath.Join(root, "gitlab"); dirExists(d) {
		owners, _ := os.ReadDir(d)
		for _, o := range owners {
			if !o.IsDir() {
				continue
			}
			ownerDir := filepath.Join(d, o.Name())
			if fileExists(filepath.Join(ownerDir, ".envrc")) {
				out = append(out, remoteTarget{Dir: ownerDir, Forge: "gitlab", Owner: o.Name()})
			}
		}
	}
	if d := filepath.Join(root, "git-forgejo"); dirExists(d) && fileExists(filepath.Join(d, ".envrc")) {
		out = append(out, remoteTarget{Dir: d, Forge: "forgejo", Owner: "freax"})
	}
	if d := filepath.Join(root, "ado"); dirExists(d) && fileExists(filepath.Join(d, ".envrc")) {
		out = append(out, remoteTarget{Dir: d, Forge: "ado"})
	}
	return out
}

// envFromDirenv reads the named env vars under dir's direnv scope. Missing vars
// come back as empty strings. Falls back to the parent process env when direnv
// is absent or fails, so tests without direnv still resolve tokens.
func envFromDirenv(dir string, vars []string) map[string]string {
	result := make(map[string]string, len(vars))
	if _, err := exec.LookPath("direnv"); err != nil {
		for _, v := range vars {
			result[v] = os.Getenv(v)
		}
		return result
	}
	var script strings.Builder
	for _, v := range vars {
		script.WriteString(`printf '%s\n' "${`)
		script.WriteString(v)
		script.WriteString(`:-}"; `)
	}
	cmd := exec.Command("direnv", "exec", dir, "sh", "-c", script.String())
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		for _, v := range vars {
			result[v] = os.Getenv(v)
		}
		return result
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for i, v := range vars {
		if i < len(lines) {
			result[v] = lines[i]
		} else {
			result[v] = ""
		}
	}
	return result
}

func fetchTargetRepos(ctx context.Context, t remoteTarget) ([]forge.RepoRef, error) {
	switch t.Forge {
	case "github":
		env := envFromDirenv(t.Dir, []string{"GH_TOKEN", "GITHUB_TOKEN"})
		tok := env["GH_TOKEN"]
		if tok == "" {
			tok = env["GITHUB_TOKEN"]
		}
		if tok == "" {
			return nil, nil
		}
		c := forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
		return c.ListRepos(ctx, t.Owner)
	case "gitlab":
		env := envFromDirenv(t.Dir, []string{"GITLAB_TOKEN"})
		tok := env["GITLAB_TOKEN"]
		if tok == "" {
			return nil, nil
		}
		c := forge.NewGitlabClient(tok, os.Getenv("BRIDGE_GITLAB_API"))
		return c.ListRepos(ctx, t.Owner)
	case "forgejo":
		env := envFromDirenv(t.Dir, []string{"FORGEJO_TOKEN"})
		tok := env["FORGEJO_TOKEN"]
		if tok == "" {
			return nil, nil
		}
		c := forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
		return c.ListRepos(ctx, t.Owner)
	case "ado":
		env := envFromDirenv(t.Dir, []string{"AZURE_DEVOPS_ORG_URL", "AZURE_DEVOPS_EXT_PAT", "ADO_PAT"})
		orgURL := env["AZURE_DEVOPS_ORG_URL"]
		if api := os.Getenv("BRIDGE_ADO_API"); api != "" {
			orgURL = api
		}
		if orgURL == "" {
			return nil, nil
		}
		tok := env["AZURE_DEVOPS_EXT_PAT"]
		if tok == "" {
			tok = env["ADO_PAT"]
		}
		if tok == "" {
			return nil, nil
		}
		c := forge.NewADOClient(tok, orgURL)
		return c.ListRepos(ctx, "")
	}
	return nil, nil
}

// ownerEnvrcDir returns the directory holding the token .envrc for a GitHub
// owner: ownerDir itself when github/<owner>/.envrc exists, else the first
// immediate subdirectory carrying an .envrc (the github/<owner>/<visibility>/
// layout). Returns "" when no marker is found.
func ownerEnvrcDir(ownerDir string) string {
	if fileExists(filepath.Join(ownerDir, ".envrc")) {
		return ownerDir
	}
	subs, _ := os.ReadDir(ownerDir)
	for _, s := range subs {
		if s.IsDir() && fileExists(filepath.Join(ownerDir, s.Name(), ".envrc")) {
			return filepath.Join(ownerDir, s.Name())
		}
	}
	return ""
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
```

- [ ] **Step 4: Repoint `cmd/bridge/list.go` at the new package**

In `cmd/bridge/list.go`, delete the moved declarations: `remoteTarget` (lines 92-101), `discoverRemoteTargets` (103-153), `envFromDirenv` (155-190), `fetchTargetRepos` (228-277), and `ownerEnvrcDir` (279-295). **Keep** `dirExists` (297-300) and `fileExists` (302-305) — `bases.go:65` still calls `dirExists`. **Keep** `const remoteTTL = time.Hour` (line 90).

Replace `loadOrFetchRemote` (lines 192-226) with the delegating version:

```go
func loadOrFetchRemote(ctx context.Context, local []core.Repo, refresh bool) ([]forge.RepoRef, error) {
	cachePath := filepath.Join(cacheRoot(), "remote.list")
	if !refresh {
		c, err := forge.ReadRepoCache(cachePath)
		if err == nil && !c.IsStale(remoteTTL) && len(c.Repos) > 0 {
			return c.Repos, nil
		}
	}
	return remote.Refresh(ctx, reposRoots(), cachePath)
}
```

Add the import `"github.com/freaxnx01/bridge/internal/remote"` to `cmd/bridge/list.go`. Then run `goimports -w cmd/bridge/list.go` to drop now-unused imports (`io`, `os/exec`, `strings`, possibly `time` if `remoteTTL` is the only remaining `time` user — keep `time` since `remoteTTL = time.Hour`). The `local` parameter stays unused exactly as before (it was already unused in the fetch path); leave the signature unchanged to avoid touching the caller.

- [ ] **Step 5: Run the new package tests and the list tests**

Run: `go test ./internal/remote/ ./cmd/bridge/ -run 'TestDiscover|TestRefresh|TestList' -v`
Expected: PASS — new tests green; existing `TestListLocalHuman`, `TestListLocalJSON`, `TestListSpansMultipleBases`, `TestListBaseFlagOverridesEnv`, `TestListMissingBaseWarns` still pass.

- [ ] **Step 6: Verify the whole tree compiles and is formatted**

Run: `gofmt -l internal/remote/ cmd/bridge/ && go build ./...`
Expected: no `gofmt` output, build succeeds.

- [ ] **Step 7: Commit**

```bash
git add internal/remote/ cmd/bridge/list.go
git commit -m "refactor(remote): extract forge fetch into internal/remote

Move remote-target discovery, direnv token resolution, and per-forge
fetch out of cmd/bridge/list.go into internal/remote.Refresh so nav can
reach it. bridge list keeps its 1h TTL gate and delegates the fetch.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Add `Config.FetchRemote` and wire it in `cmd/bridge/nav.go`

**Files:**
- Modify: `internal/nav/types.go:153-180` (Config struct)
- Modify: `cmd/bridge/nav.go:23-59` (cfg literal) + imports

- [ ] **Step 1: Add the field to `Config`**

In `internal/nav/types.go`, inside the `Config` struct (after the `FetchIssues` field, near line 174), add:

```go
	// FetchRemote re-queries every configured forge and returns the owned
	// repos, also refreshing the on-disk cache. Nil disables live refresh: the
	// r key falls back to re-reading the cache.
	FetchRemote func(ctx context.Context) ([]forge.RepoRef, error)
```

`internal/nav/types.go` already imports `context` and `github.com/freaxnx01/bridge/internal/forge` (used by `FetchIssues`). Verify with `goimports -l internal/nav/types.go` (expect no output).

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/nav/`
Expected: builds (new field, no consumers yet).

- [ ] **Step 3: Wire the callback in `cmd/bridge/nav.go`**

In `cmd/bridge/nav.go`, add to the `nav.Config{...}` literal (after the `FetchIssues` block, before `IssueCacheDir`):

```go
			FetchRemote: func(ctx context.Context) ([]forge.RepoRef, error) {
				return remote.Refresh(ctx, reposRoots(), filepath.Join(cacheRoot(), "remote.list"))
			},
```

Add the import `"github.com/freaxnx01/bridge/internal/remote"` to `cmd/bridge/nav.go` (`context`, `path/filepath`, and `forge` are already imported).

- [ ] **Step 4: Verify the binary builds**

Run: `gofmt -l internal/nav/types.go cmd/bridge/nav.go && go build ./...`
Expected: no `gofmt` output, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/types.go cmd/bridge/nav.go
git commit -m "feat(nav): add Config.FetchRemote DI callback

Mirror the existing Clone/FetchIssues callbacks: nav reaches the forge
fetch through Config.FetchRemote, wired in cmd/bridge to remote.Refresh,
keeping internal/nav free of direnv/forge machinery.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Make the picker `r` key re-fetch

**Files:**
- Modify: `internal/nav/data.go` (add `refreshRemoteCmd`)
- Modify: `internal/nav/update.go:307-309` (`r` handler)
- Modify: `internal/nav/update_test.go` (new tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go`:

```go
func TestUpdatePicker_R_WithFetchRemote_BuildsRemoteRows(t *testing.T) {
	m := initialModel(Config{
		FetchRemote: func(_ context.Context) ([]forge.RepoRef, error) {
			return []forge.RepoRef{
				{Forge: "github", Owner: "acme", Name: "zeta"},
				{Forge: "github", Owner: "acme", Name: "alpha"},
			}, nil
		},
	})
	m.pickerFocus = focusList
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if got := out.(Model).remoteState; got != loadPending {
		t.Fatalf("remoteState = %d, want loadPending while fetching", got)
	}
	if cmd == nil {
		t.Fatal("r should return a fetch Cmd")
	}
	msg := cmd()
	rm, ok := msg.(remoteMsg)
	if !ok {
		t.Fatalf("cmd msg = %T, want remoteMsg", msg)
	}
	if len(rm.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rm.rows))
	}
	if !strings.HasPrefix(rm.rows[0].label, "↓ ") {
		t.Errorf("row 0 label = %q, want ↓ prefix", rm.rows[0].label)
	}
	// sortRepoRows orders rows; alpha must precede zeta.
	if !strings.Contains(rm.rows[0].label, "alpha") {
		t.Errorf("rows not sorted: row 0 = %q", rm.rows[0].label)
	}
}

func TestUpdatePicker_R_FetchError_YieldsRemoteErr(t *testing.T) {
	m := initialModel(Config{
		FetchRemote: func(_ context.Context) ([]forge.RepoRef, error) {
			return nil, errFake
		},
	})
	m.pickerFocus = focusList
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r should return a Cmd")
	}
	if _, ok := cmd().(remoteErrMsg); !ok {
		t.Fatalf("cmd msg = %T, want remoteErrMsg", cmd())
	}
}

func TestUpdatePicker_R_NilFetchRemote_FallsBackToCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	if err := forge.WriteRepoCache(cachePath, forge.RepoCache{
		Repos: []forge.RepoRef{{Forge: "github", Owner: "acme", Name: "cached"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := initialModel(Config{RemoteCache: cachePath}) // FetchRemote nil
	m.pickerFocus = focusList
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r should return a Cmd even with nil FetchRemote")
	}
	rm, ok := cmd().(remoteMsg)
	if !ok {
		t.Fatalf("cmd msg = %T, want remoteMsg from cache", cmd())
	}
	if len(rm.rows) != 1 || !strings.Contains(rm.rows[0].label, "cached") {
		t.Errorf("fallback did not read cache: %+v", rm.rows)
	}
}
```

`update_test.go` already imports `forge`, `tea`, `strings`, `path/filepath`, and `testing`; add `"context"` to its import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav/ -run TestUpdatePicker_R_ -v`
Expected: FAIL — `m.refreshRemoteCmd` undefined (the `r` case still calls `loadRemoteCmd`, so the `remoteMsg`/`remoteErrMsg` assertions fail) and/or compile error referencing the not-yet-added method.

- [ ] **Step 3: Add `refreshRemoteCmd` to `internal/nav/data.go`**

Append to `internal/nav/data.go` (it already imports `context`, `tea`, and `forge`):

```go
// refreshRemoteCmd re-queries the forge via the injected FetchRemote callback
// and rebuilds the ↓-prefixed remote rows. When FetchRemote is nil (callback
// not wired), it falls back to re-reading the on-disk cache.
func (m Model) refreshRemoteCmd() tea.Cmd {
	if m.cfg.FetchRemote == nil {
		return loadRemoteCmd(m.cfg.RemoteCache)
	}
	fetch := m.cfg.FetchRemote
	return func() tea.Msg {
		refs, err := fetch(context.Background())
		if err != nil {
			return remoteErrMsg{err: err}
		}
		rows := make([]repoRow, 0, len(refs))
		for i := range refs {
			ref := refs[i]
			rows = append(rows, repoRow{label: "↓ " + remoteLabel(ref), remote: &ref})
		}
		sortRepoRows(rows)
		return remoteMsg{rows: rows}
	}
}
```

- [ ] **Step 4: Point the `r` handler at it**

In `internal/nav/update.go`, replace the `case "r"` body (lines 307-309):

```go
	case "r":
		m.remoteState = loadPending
		return m, m.refreshRemoteCmd()
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run TestUpdatePicker_R_ -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/nav/data.go internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): picker r re-fetches remotes from the forge

r now runs Config.FetchRemote (re-querying the forge and refreshing the
cache) instead of only re-reading remote.list, so newly-created repos
appear without leaving nav. Nil callback falls back to the cache read.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Document `r` in the picker hint line

**Files:**
- Modify: `internal/nav/view.go:138`
- Modify: `internal/nav/view_test.go` (if a hint-line assertion exists)

- [ ] **Step 1: Check for an existing hint-line test**

Run: `grep -n "open/attach\|hintLine\|tab panes" internal/nav/view_test.go`
Expected: either a test asserting the picker hint string, or no match.

- [ ] **Step 2: Update the hint line**

In `internal/nav/view.go`, change the picker hint (line 138) from:

```go
	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · tab panes · q quit"))
```

to:

```go
	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · tab panes · q quit"))
```

If Step 1 found a test asserting the old string, update that expected string to match (add `· r refresh` in the same position). If no such test exists, add none — the hint line is presentational and covered by the existing render smoke tests.

- [ ] **Step 3: Verify build + format + any view test**

Run: `gofmt -l internal/nav/view.go && go test ./internal/nav/ -run TestView -v`
Expected: no `gofmt` output; view tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "docs(nav): document r refresh in picker hint line

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

(If `view_test.go` was not modified, drop it from the `git add`.)

---

## Task 5: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Format check**

Run: `gofmt -l .`
Expected: empty output.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: clean (no output).

- [ ] **Step 3: Lint**

Run: `golangci-lint run`
Expected: clean. If `errcheck` flags the `_ = forge.WriteRepoCache(...)` in `internal/remote/remote.go`, confirm it carries the explanatory comment from Task 1 Step 3 (the same construct passed lint in `list.go` previously); do not add `//nolint`.

- [ ] **Step 4: Full race suite**

Run: `go test -race ./...`
Expected: all packages pass, including `internal/remote`, `internal/nav`, `cmd/bridge`.

- [ ] **Step 5: Manual smoke (real forge)**

Run:
```bash
just build
bridge nav
```
Then: confirm the picker hint shows `r refresh`; press `r`; the title shows the loading spinner, then the list re-renders with `↓`-prefixed remote repos. Confirm a repo you know exists on the forge (e.g. `quotes`) appears. Press `q` to exit.
Expected: remote repos refresh in-place; `~/.cache/bridge/remote.list` is updated (`ls -l` shows a fresh mtime).

- [ ] **Step 6: Final confirmation**

Report results of Steps 1-5 with the actual command output. Do not claim success without the output (per the verification-before-completion discipline).

---

## Notes for the implementer

- **Do not** change `bridge list`'s observable behavior or its `remoteTTL` (1h). Task 1 is a pure extraction + delegation.
- **Do not** add a dashboard refresh key — out of scope (spec decision 4).
- **Do not** add new dependencies or change the cache file format/path.
- The `↓ ` prefix, `remoteLabel`, and `sortRepoRows` in `refreshRemoteCmd` must match `loadRemoteCmd` (`internal/nav/data.go:76-90`) exactly — they produce the same row shape so cached and freshly-fetched rows render identically.
- `remoteMsg` / `remoteErrMsg` are already handled in `internal/nav/update.go:29-36`; the picker title spinner (`loadPending`) and "remote unavailable (cached rows shown)" status (`loadErr`) are already wired. This plan adds no new message types and no new view states.
- If you hit a blocker, find the fix and note it inline here for the next run.
