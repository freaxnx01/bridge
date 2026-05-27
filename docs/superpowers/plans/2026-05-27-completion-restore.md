# Restore repo-name tab-completion — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore bash + PowerShell tab-completion for repo names on `bridge open`, `bridge rm`, and at the root, via a Cobra `ValidArgsFunction`. Closes [#65](https://github.com/freaxnx01/bridge/issues/65).

**Architecture:** A pure `internal/completion.Resolve` function does three-stage matching (basename prefix → basename substring → meta-keyword fallback over `repo-meta.json`). A thin `cmd/bridge/completion.go` adapter loads the data via the existing `core.DiscoverRepos` walker and `core.LoadRepoMeta` reader, then calls `Resolve`. The adapter is registered as the `ValidArgsFunction` on three Cobra commands; Cobra emits the shell-specific completion scripts for free.

**Tech Stack:** Go 1.x, [`github.com/spf13/cobra`](https://github.com/spf13/cobra) (already a dep), bats for shim tests.

**Spec:** [`docs/superpowers/specs/2026-05-27-completion-restore-design.md`](../specs/2026-05-27-completion-restore-design.md).

---

## File Structure

**Created:**
- `internal/completion/completion.go` — pure `Resolve(prefix, repos, meta) []string`.
- `internal/completion/completion_test.go` — table-driven unit tests.
- `cmd/bridge/completion.go` — Cobra adapter `repoNameCompletion`; loads repos/meta and calls `Resolve`.
- `cmd/bridge/completion_test.go` — integration tests invoking `bridge __complete ...` as a subprocess.

**Modified:**
- `cmd/bridge/open.go` — assign `openCmd.ValidArgsFunction = repoNameCompletion`.
- `cmd/bridge/rm.go` — assign `rmCmd.ValidArgsFunction = repoNameCompletion`.
- `cmd/bridge/root.go` — assign `rootCmd.ValidArgsFunction = rootRepoNameCompletion` (the verb-filtering variant).
- `shims/bridge-shim.bats` — add an end-to-end completion smoke test.
- `README.md` — add a *Completion* section with bash + PowerShell install snippets.

---

## Task 1: Pure resolver in `internal/completion`

**Files:**
- Create: `internal/completion/completion.go`
- Test: `internal/completion/completion_test.go`

- [ ] **Step 1: Create the test file with table-driven tests**

Write `internal/completion/completion_test.go`:

```go
package completion

import (
	"reflect"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestResolve(t *testing.T) {
	// meta is keyed by Path (matches what core.LoadRepoMeta returns), so the
	// resolver joins meta to repos via r.Path. Give each repo a Path that
	// matches a meta key for the rows that exercise meta fallback.
	repos := []core.Repo{
		{Name: "bridge", Path: "github/freaxnx01/public/bridge"},
		{Name: "ArchiveRestApiNextGen", Path: "github/freaxnx01/public/ArchiveRestApiNextGen"},
		{Name: "BI-ArchiveUploader", Path: "github/freaxnx01/public/BI-ArchiveUploader"},
		{Name: "config", Path: "github/freaxnx01/public/config"},
	}
	meta := map[string]core.RepoMeta{
		"github/freaxnx01/public/ArchiveRestApiNextGen": {
			Description: "next-gen archive REST API",
			Topics:      []string{"archive", "rest", "next-gen"},
		},
		"github/freaxnx01/public/bridge": {
			Description: "repo picker",
			Topics:      []string{"dev-tools", "cli"},
		},
	}

	cases := []struct {
		name   string
		prefix string
		want   []string
	}{
		{"empty prefix returns all sorted", "", []string{"ArchiveRestApiNextGen", "BI-ArchiveUploader", "bridge", "config"}},
		{"prefix match wins over substring", "br", []string{"bridge"}},
		{"prefix match is case-insensitive", "BR", []string{"bridge"}},
		{"substring match when no prefix", "archive", []string{"ArchiveRestApiNextGen", "BI-ArchiveUploader"}},
		{"substring match is case-insensitive", "ARCHIVE", []string{"ArchiveRestApiNextGen", "BI-ArchiveUploader"}},
		{"meta fallback by topic", "nextgen", []string{"ArchiveRestApiNextGen"}},
		{"meta fallback by description", "picker", []string{"bridge"}},
		{"no match anywhere", "zzz", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.prefix, repos, meta)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Resolve(%q) = %v, want %v", tc.prefix, got, tc.want)
			}
		})
	}
}

func TestResolveDeduplicates(t *testing.T) {
	// A repo whose basename AND meta both match the same prefix must appear once.
	repos := []core.Repo{
		{Name: "archive", Path: "github/freaxnx01/public/archive"},
	}
	meta := map[string]core.RepoMeta{
		"github/freaxnx01/public/archive": {
			Description: "archive tool",
			Topics:      []string{"archive"},
		},
	}
	got := Resolve("archive", repos, meta)
	want := []string{"archive"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/completion/...
```

Expected: build failure — `package completion` not yet defined.

- [ ] **Step 3: Write the resolver**

Create `internal/completion/completion.go`:

```go
// Package completion produces tab-completion candidates for repo names.
//
// Resolve is a pure function: callers pre-load repos via core.DiscoverRepos
// and meta via core.LoadRepoMeta, then ask Resolve to filter+rank.
package completion

import (
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
)

// Resolve returns the first non-empty stage of candidate repo names for prefix:
//
//  1. Case-insensitive basename prefix match.
//  2. Case-insensitive basename substring match.
//  3. Meta-keyword fallback: case-insensitive substring against description
//     and topics in meta (keyed by repo path).
//
// Empty prefix returns all repo names. Returned names are sorted
// case-insensitively and deduplicated. Never returns an error.
func Resolve(prefix string, repos []core.Repo, meta map[string]core.RepoMeta) []string {
	needle := strings.ToLower(prefix)

	var prefixHits, substrHits []string
	for _, r := range repos {
		lname := strings.ToLower(r.Name)
		switch {
		case strings.HasPrefix(lname, needle):
			prefixHits = append(prefixHits, r.Name)
		case strings.Contains(lname, needle):
			substrHits = append(substrHits, r.Name)
		}
	}
	if len(prefixHits) > 0 {
		return sortDedup(prefixHits)
	}
	if len(substrHits) > 0 {
		return sortDedup(substrHits)
	}

	if needle == "" || len(meta) == 0 {
		return nil
	}
	var metaHits []string
	for _, r := range repos {
		m, ok := meta[r.Path]
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(m.Description), needle) {
			metaHits = append(metaHits, r.Name)
			continue
		}
		for _, t := range m.Topics {
			if strings.Contains(strings.ToLower(t), needle) {
				metaHits = append(metaHits, r.Name)
				break
			}
		}
	}
	return sortDedup(metaHits)
}

func sortDedup(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool {
		return strings.ToLower(in[i]) < strings.ToLower(in[j])
	})
	out := in[:0]
	var last string
	for _, s := range in {
		if s == last {
			continue
		}
		out = append(out, s)
		last = s
	}
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/completion/... -v
```

Expected: all subtests PASS, including `TestResolveDeduplicates`.

- [ ] **Step 5: Commit**

```bash
git add internal/completion/
git commit -m "feat(completion): pure Resolve with prefix/substring/meta stages (#65)"
```

---

## Task 2: Cobra adapter + wire `openCmd`

**Files:**
- Create: `cmd/bridge/completion.go`
- Create: `cmd/bridge/completion_test.go`
- Modify: `cmd/bridge/open.go` (one line)

- [ ] **Step 1: Write the failing integration test**

Create `cmd/bridge/completion_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runComplete invokes `bridge __complete <subcommand> <prefix>` against a
// repos root and cache root, and returns the candidate names emitted before
// the trailing ":<directive>" line that cobra writes.
func runComplete(t *testing.T, repoRoot, cacheRoot string, args ...string) []string {
	t.Helper()
	full := append([]string{"__complete"}, args...)
	cmd := bridgeCmd(full...)
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+repoRoot,
		"XDG_CACHE_HOME="+cacheRoot,
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("__complete %v: %v\n%s", args, err, sout.String())
	}
	var out []string
	for _, line := range strings.Split(sout.String(), "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		// cobra emits "<name>\t<description>" — take the name only.
		name, _, _ := strings.Cut(line, "\t")
		out = append(out, name)
	}
	return out
}

func TestCompleteOpenBasenamePrefix(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	got := runComplete(t, root, cache, "open", "br")
	if !containsAll(got, []string{"bridge"}) {
		t.Errorf("got %v, want to contain bridge", got)
	}
}

func TestCompleteOpenEmptyPrefixListsAll(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	got := runComplete(t, root, cache, "open", "")
	if !containsAll(got, []string{"bridge", "secret", "glrepo"}) {
		t.Errorf("got %v, want all three repos", got)
	}
}

func TestCompleteOpenMetaFallback(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)
	_ = os.WriteFile(filepath.Join(bridgeCache, "repo-meta.json"), []byte(`{
		"github/freaxnx01/public/bridge": {
			"description": "repo picker",
			"topics": ["dev-tools","cli"]
		}
	}`), 0o644)
	got := runComplete(t, root, cache, "open", "picker")
	if !containsAll(got, []string{"bridge"}) {
		t.Errorf("got %v, want bridge via meta fallback", got)
	}
}

func containsAll(haystack, needles []string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./cmd/bridge/ -run TestCompleteOpen -v
```

Expected: FAIL — `bridge open <tab>` returns no candidates (no `ValidArgsFunction` registered yet).

- [ ] **Step 3: Write the Cobra adapter**

Create `cmd/bridge/completion.go`:

```go
package main

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/completion"
	"github.com/freaxnx01/bridge/internal/core"
)

// repoNameCompletion is the ValidArgsFunction used by open/rm and (via a
// thin wrapper) the root command. It fires only for the first positional;
// further args complete to nothing.
func repoNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	repos, err := core.DiscoverRepos(reposRoot())
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	// LoadRepoMeta returns an empty map on missing/empty file with no error,
	// so meta-fallback gracefully no-ops until #<follow-up> restores the writer.
	meta, _ := core.LoadRepoMeta(filepath.Join(cacheRoot(), "repo-meta.json"))
	return completion.Resolve(toComplete, repos, meta), cobra.ShellCompDirectiveNoFileComp
}
```

- [ ] **Step 4: Wire `openCmd`**

Edit `cmd/bridge/open.go`. In the `var openCmd = &cobra.Command{...}` block, add the `ValidArgsFunction`:

```go
var openCmd = &cobra.Command{
	Use:               "open <name>",
	Short:             "Open a repo (creates/attaches an agent session in Phase 2)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: repoNameCompletion,
	RunE:              runOpen,
}
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
go test ./cmd/bridge/ -run TestCompleteOpen -v
```

Expected: all three `TestCompleteOpen*` PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/completion.go cmd/bridge/completion_test.go cmd/bridge/open.go
git commit -m "feat(completion): wire ValidArgsFunction on bridge open (#65)"
```

---

## Task 3: Wire `rmCmd`

**Files:**
- Modify: `cmd/bridge/rm.go` (one line)
- Modify: `cmd/bridge/completion_test.go` (add test)

- [ ] **Step 1: Add a failing test**

Append to `cmd/bridge/completion_test.go`:

```go
func TestCompleteRmBasenamePrefix(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	got := runComplete(t, root, cache, "rm", "se")
	if !containsAll(got, []string{"secret"}) {
		t.Errorf("got %v, want to contain secret", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./cmd/bridge/ -run TestCompleteRm -v
```

Expected: FAIL — `rmCmd` has no `ValidArgsFunction`.

- [ ] **Step 3: Wire `rmCmd`**

Edit `cmd/bridge/rm.go`. Update the `rmCmd` declaration:

```go
var rmCmd = &cobra.Command{
	Use:               "rm <name>",
	Short:             "Delete a local repo (refuses without --yes if not a TTY)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: repoNameCompletion,
	RunE:              runRm,
}
```

- [ ] **Step 4: Run to verify pass**

```bash
go test ./cmd/bridge/ -run TestCompleteRm -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/rm.go cmd/bridge/completion_test.go
git commit -m "feat(completion): wire ValidArgsFunction on bridge rm (#65)"
```

---

## Task 4: Wire `rootCmd` with verb filter

**Files:**
- Modify: `cmd/bridge/root.go`
- Modify: `cmd/bridge/completion.go` (add `rootRepoNameCompletion`)
- Modify: `cmd/bridge/completion_test.go` (add tests)

- [ ] **Step 1: Add failing tests**

Append to `cmd/bridge/completion_test.go`:

```go
func TestCompleteRootSuggestsRepos(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	got := runComplete(t, root, cache, "br")
	if !containsAll(got, []string{"bridge"}) {
		t.Errorf("got %v, want bridge at root", got)
	}
}

func TestCompleteRootHidesKnownVerbs(t *testing.T) {
	// Create a repo whose basename collides with a known verb. The root-level
	// filter must hide it (so `bridge li<tab>` still surfaces the `list`
	// subcommand without ambiguity).
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "github/freaxnx01/public/list"))
	mustMkdir(t, filepath.Join(root, "github/freaxnx01/public/bridge"))
	cache := t.TempDir()
	got := runComplete(t, root, cache, "li")
	for _, c := range got {
		if c == "list" {
			t.Errorf("root completion leaked verb name %q via repo basename", c)
		}
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./cmd/bridge/ -run TestCompleteRoot -v
```

Expected: FAIL — root has no `ValidArgsFunction`.

- [ ] **Step 3: Add the root-level adapter**

Append to `cmd/bridge/completion.go`:

```go
// rootRepoNameCompletion wraps repoNameCompletion and filters out candidates
// whose name exactly matches a known subcommand verb. Without this, cloning a
// repo called `list` would override `bridge li<tab>` -> `list`.
func rootRepoNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	names, dir := repoNameCompletion(cmd, args, toComplete)
	if len(names) == 0 {
		return names, dir
	}
	out := names[:0]
	for _, n := range names {
		if knownVerbs[n] {
			continue
		}
		out = append(out, n)
	}
	return out, dir
}
```

- [ ] **Step 4: Wire `rootCmd`**

Edit `cmd/bridge/root.go`. Update the declaration:

```go
var rootCmd = &cobra.Command{
	Use:   "bridge",
	Short: "Repo picker + agent launcher (Go core)",
	Long: `bridge is a repo picker and agent-session launcher. It walks
~/projects/repos/, presents an fzf picker, then opens the selected repo in
a tmux-wrapped agent session (Claude Code, Copilot, opencode, VS Code) or
cd's into it via the shell shim.

Cache lives at ~/.cache/bridge/ (overridable via XDG_CACHE_HOME).
Repo discovery walks ~/projects/repos/ (overridable via BRIDGE_REPOS_ROOT).`,
	Version:           versionString(),
	ValidArgsFunction: rootRepoNameCompletion,
}
```

- [ ] **Step 5: Run to verify pass**

```bash
go test ./cmd/bridge/ -run TestCompleteRoot -v
```

Expected: both `TestCompleteRoot*` PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/root.go cmd/bridge/completion.go cmd/bridge/completion_test.go
git commit -m "feat(completion): wire root ValidArgsFunction with verb filter (#65)"
```

---

## Task 5: Bash shim end-to-end smoke test

**Files:**
- Modify: `shims/bridge-shim.bats`

- [ ] **Step 1: Add a bats test that exercises `__complete` end-to-end through the shim**

Append to `shims/bridge-shim.bats`:

```bash
@test "completion: __complete open <prefix> returns matching repo basenames (#65)" {
    # Create a fixture repos root with two repos. The completion handler
    # walks BRIDGE_REPOS_ROOT directly, so no cache priming is needed.
    repos=$(mktemp -d)
    mkdir -p "$repos/github/freaxnx01/public/bridge"
    mkdir -p "$repos/github/freaxnx01/public/ArchiveRestApiNextGen"
    cache=$(mktemp -d)

    run env BRIDGE_REPOS_ROOT="$repos" XDG_CACHE_HOME="$cache" \
        bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge __complete open br"

    [ "$status" -eq 0 ]
    [[ "$output" == *"bridge"* ]]
    [[ "$output" != *"ArchiveRestApiNextGen"* ]] # prefix stage, not substring

    rm -rf "$repos" "$cache"
}
```

- [ ] **Step 2: Run the bats suite**

```bash
bats shims/bridge-shim.bats
```

Expected: all tests PASS, including the new completion test.

- [ ] **Step 3: Commit**

```bash
git add shims/bridge-shim.bats
git commit -m "test(completion): bats smoke test for __complete through shim (#65)"
```

---

## Task 6: README install snippets

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Locate the install / shell-integration section**

```bash
grep -n "^##\|^# " README.md | head -30
```

Find the section after the install/shim section — completion belongs alongside the shim install steps.

- [ ] **Step 2: Add a Completion section**

Insert a new section after the existing shim install instructions (use `Edit` to place it before the next `## ` heading):

```markdown
## Tab completion

bridge ships Cobra-generated completion for both shells. Install the script
once; thereafter `bridge open <tab>`, `bridge rm <tab>`, and `bridge <tab>`
(implicit `open`) complete against local repos under `~/projects/repos/`.

**bash** (`.bashrc`):

```bash
eval "$(bridge completion bash)"
```

Or, for a system-wide install:

```bash
bridge completion bash | sudo tee /etc/bash_completion.d/bridge >/dev/null
```

**PowerShell** (`$PROFILE`):

```powershell
bridge completion powershell | Out-String | Invoke-Expression
```

Completion matches in three stages: case-insensitive basename prefix, then
basename substring, then a keyword fallback against the description and
topics in `~/.cache/bridge/repo-meta.json`. The basename layer is always
fresh (it re-walks `~/projects/repos/`); the meta layer is populated by
`bridge list -r --meta` (see [follow-up issue](https://github.com/freaxnx01/bridge/issues/<TBD>)).
```

> When committing this task, replace `<TBD>` with the actual follow-up
> issue number created in Task 8.

- [ ] **Step 3: Verify the README renders correctly**

```bash
grep -n "Tab completion" README.md
```

Expected: the new heading appears once.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: install snippets for bash + PowerShell tab completion (#65)"
```

---

## Task 7: Full verification + cleanup

**Files:** None modified — verification only.

- [ ] **Step 1: Run the full Go test suite**

```bash
go test ./...
```

Expected: PASS across all packages, including `internal/completion`, `cmd/bridge`, and pre-existing tests.

- [ ] **Step 2: Run the bats suite**

```bash
bats shims/bridge-shim.bats
```

Expected: PASS.

- [ ] **Step 3: Build and exercise the binary manually**

```bash
make install-go
bridge --version
```

Then in a fresh interactive bash:

```bash
source ~/.bashrc  # or wherever the shim is loaded
eval "$(bridge completion bash)"
bridge open br<TAB>
```

Expected: completion expands to `bridge`. Try `bridge nextgen<TAB>` (will only resolve if `repo-meta.json` is populated — otherwise empty is correct).

- [ ] **Step 4: Manually verify PowerShell completion (Windows machine)**

Document the manual verification in the PR description as the PR template requires:

```
- [x] Built on Windows: `go build ./cmd/bridge`
- [x] Loaded shim + completion in pwsh:
        bridge completion powershell | Out-String | Invoke-Expression
- [x] `bridge open br<TAB>` -> bridge
- [x] `bridge rm br<TAB>` -> bridge
- [x] `bridge br<TAB>` -> bridge (root-level)
```

If no Windows machine is available right now, note that explicitly in the PR description and flag for review.

- [ ] **Step 5: Confirm clean working tree**

```bash
git status
```

Expected: clean (no uncommitted changes).

---

## Task 8: File follow-up issue for `repo-meta.json` writer

**Files:** None — uses `gh`.

- [ ] **Step 1: Verify nothing in the Go code writes `repo-meta.json`**

```bash
grep -rln "repo-meta\\.json" cmd/ internal/
```

Expected: hits are all readers (`internal/core/repo_meta.go`, `cmd/bridge/open.go`, the new `cmd/bridge/completion.go`). No writer.

- [ ] **Step 2: Create the follow-up issue**

```bash
gh issue create --title "feat(meta-cache): restore repo-meta.json writer for forge metadata" --body "$(cat <<'EOF'
## Why

Issue #65 restored tab-completion with a three-stage matcher: basename
prefix, basename substring, then a meta-keyword fallback against
`~/.cache/bridge/repo-meta.json` (description + topics). The completion
code is wired and tested, but the meta-fallback stage is a no-op today
because **nothing in the Go binary writes `repo-meta.json`** — the
writer lived in the deleted bash code (Phase 4, #35).

Result: `bridge open nextgen<tab>` will not resolve to
`ArchiveRestApiNextGen` until this issue lands, even though the
completion code expects it to.

## Scope

- Restore a writer that fetches description + topics from each configured
  forge (GitHub, GitLab, Forgejo) and persists to
  `~/.cache/bridge/repo-meta.json` keyed by repo path relative to
  `reposRoot()`.
- Likely surface: a new `--meta` flag on `bridge list -r`, or a new
  `bridge sync --meta` subverb. Pick whichever fits the existing forge
  client pattern (`internal/forge`).
- The JSON schema is already defined by
  [`internal/core/repo_meta.go`](internal/core/repo_meta.go) — match it.

## Acceptance criteria

- [ ] Running the writer populates `repo-meta.json` with description +
      topics for every local repo whose remote can be fetched.
- [ ] `bridge open nextgen<tab>` resolves to
      `ArchiveRestApiNextGen` (or equivalent) after the writer runs.
- [ ] Cache TTL / refresh semantics documented (mirror `remote.list`?
      see `loadOrFetchRemote` in `cmd/bridge/list.go:96-150`).
- [ ] Unit tests for the writer with an `httptest.Server` stand-in
      for the forge APIs.

## References

- Issue #65 (the consumer).
- `internal/forge/` — existing forge clients with `ListRepos`.
- `internal/core/repo_meta.go` — schema + reader.
- `cmd/bridge/list.go:96-150` — pattern for cache-with-TTL.
EOF
)"
```

- [ ] **Step 3: Capture the new issue number and back-link the README**

The previous task left `<TBD>` in `README.md`. Update it:

```bash
gh issue list --state open --limit 5  # confirm the new issue number
```

Then use `Edit` on `README.md` to replace `https://github.com/freaxnx01/bridge/issues/<TBD>` with the actual URL.

- [ ] **Step 4: Commit the README fix-up**

```bash
git add README.md
git commit -m "docs: link follow-up issue for repo-meta.json writer (#65)"
```

- [ ] **Step 5: (Optional) Open the PR**

```bash
gh pr create --title "feat(completion): restore repo-name tab-completion (#65)" --body "$(cat <<'EOF'
## Summary
- Pure three-stage resolver in `internal/completion` (prefix → substring → meta-keyword).
- Cobra adapter wired into `open`, `rm`, and root with verb filtering.
- Bats smoke test for end-to-end behaviour through the shim.
- README updated with bash + PowerShell install snippets.

Closes #65. Follow-up #<N> tracks the `repo-meta.json` writer; until that
lands, the meta-fallback stage is a graceful no-op.

## Test plan
- [x] `go test ./...` passes.
- [x] `bats shims/bridge-shim.bats` passes.
- [x] Manual: `bridge open br<TAB>` -> bridge (bash).
- [ ] Manual: same in PowerShell on Windows.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review checklist (for the implementer)

After all tasks are done, before requesting review, verify:

1. **Spec coverage:** every acceptance criterion in the spec is exercised by a test or documented as deferred to the follow-up.
2. **No placeholders:** grep the diff for `TBD`, `TODO`, `xxx` — none should remain in code; the README's `<TBD>` should be replaced in Task 8 Step 3.
3. **Signatures match:** `repoNameCompletion`, `rootRepoNameCompletion`, and `completion.Resolve` are referenced with the same names everywhere.
4. **No drift from spec:** no extra refactors slipped in (e.g. don't restructure `knownVerbs`, don't touch `findRepoByName`).
