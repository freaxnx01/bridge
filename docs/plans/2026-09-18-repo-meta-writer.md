# repo-meta.json writer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every path that refreshes the remote repo list also refreshes `repo-meta.json`, so `bridge open <keyword>` and tab-completion keyword search stop reading a cache frozen in the bash era.

**Architecture:** `core` gains the two halves it is missing — an exported key function (`RepoMetaKey`, wrapping the `bestRelUnder` that `MergeRepoMeta` already uses on read) and an atomic writer (`SaveRepoMeta`). `internal/remote` builds the path-keyed map by pairing the refs it just fetched with the local clones under the same roots, merging over the existing cache so a failed forge loses nothing, and `Refresh` writes it beside `remote.list` — which covers `list -r --refresh`, `bridge --refresh` and nav's `r`/`ctrl+r` in one place.

**Tech Stack:** Go (stdlib `encoding/json`, `net/http/httptest`), `internal/store.AtomicWrite`, stdlib `testing` with hand-rolled fakes. No new dependencies.

**Spec:** [`docs/specs/2026-09-18-repo-meta-writer-design.md`](../specs/2026-09-18-repo-meta-writer-design.md)

## Global Constraints

- **No new Go modules.** Stdlib plus packages already imported.
- **`internal/core` must not import `internal/forge`.** The map is built in `internal/remote`, which already depends on both. A `core` → `forge` import is a plan failure, not a shortcut.
- **One key formula.** The repo-meta key is the repo's shortest non-escaping path relative to any root — `bestRelUnder` (`internal/core/repo_meta.go:79`). Never write a second implementation of it.
- **The on-disk file shape is preserved.** Existing entries carry `description`, `topics`, `default_branch`, `remote_url`, `fetched_at`; the stale cache must upgrade in place, not be invalidated.
- **Cache writes are best-effort** — a write failure must never turn a successful refresh into an error.
- **Hand-rolled fakes only** — no `testify`/`mockery`/`gomock`. Table-driven with `t.Run` subtests; name tests `TestFunc_StateUnderTest_ExpectedBehavior`.
- After every task: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

---

### Task 1: `core` gains the key function, the writer, and `fetched_at`

`core` can read `repo-meta.json` but not write it, and it resolves the entry key only privately. This task exposes both halves without touching any consumer.

**Files:**
- Modify: `internal/core/repo_meta.go:11-17` (`RepoMeta` struct), and add two functions
- Test: `internal/core/repo_meta_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `RepoMeta.FetchedAt int64` with tag `json:"fetched_at,omitempty"` — Unix seconds.
  - `func RepoMetaKey(roots []string, repoPath string) string` — the repo's shortest non-escaping path relative to any root; falls back to `repoPath` when no root matches.
  - `func SaveRepoMeta(path string, meta map[string]RepoMeta) error` — atomic JSON write.

- [ ] **Step 1: Write the failing tests**

Append to `internal/core/repo_meta_test.go`:

```go
func TestRepoMetaKey_MatchesMergeRepoMetaLookup(t *testing.T) {
	roots := []string{"/home/u/repos", "/home/u/other"}
	tests := []struct {
		name string
		path string
		want string
	}{
		{"under first root", "/home/u/repos/github/acme/public/bridge", "github/acme/public/bridge"},
		{"under second root", "/home/u/other/gitlab/acme/thing", "gitlab/acme/thing"},
		{"under no root falls back to the path", "/elsewhere/repo", "/elsewhere/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RepoMetaKey(roots, tt.path); got != tt.want {
				t.Errorf("RepoMetaKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveRepoMeta_RoundTripsThroughLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo-meta.json")
	in := map[string]RepoMeta{
		"github/acme/public/bridge": {
			Description:   "repo navigator",
			Topics:        []string{"go", "tui"},
			DefaultBranch: "main",
			RemoteURL:     "git@github.com:acme/bridge.git",
			FetchedAt:     1789000000,
		},
	}
	if err := SaveRepoMeta(path, in); err != nil {
		t.Fatalf("SaveRepoMeta: %v", err)
	}
	got, err := LoadRepoMeta(path)
	if err != nil {
		t.Fatalf("LoadRepoMeta: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, in)
	}
}

func TestSaveRepoMeta_WrittenFileFeedsMergeRepoMeta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "repo-meta.json")
	repoPath := filepath.Join(root, "github", "acme", "public", "bridge")
	if err := SaveRepoMeta(path, map[string]RepoMeta{
		RepoMetaKey([]string{root}, repoPath): {
			Description:   "repo navigator",
			Topics:        []string{"go"},
			DefaultBranch: "main",
			RemoteURL:     "git@github.com:acme/bridge.git",
		},
	}); err != nil {
		t.Fatalf("SaveRepoMeta: %v", err)
	}
	meta, err := LoadRepoMeta(path)
	if err != nil {
		t.Fatalf("LoadRepoMeta: %v", err)
	}
	// The write side and the read side must agree on the key, or the merge
	// silently does nothing — which is the bug this whole change fixes.
	out := MergeRepoMeta([]Repo{{Name: "bridge", Path: repoPath}}, []string{root}, meta)
	if out[0].Desc != "repo navigator" || out[0].DefaultBranch != "main" {
		t.Errorf("merge did not populate from the written file: %+v", out[0])
	}
	if len(out[0].Topics) != 1 || out[0].Topics[0] != "go" {
		t.Errorf("topics not merged: %+v", out[0].Topics)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/ -run 'RepoMetaKey|SaveRepoMeta' -v`
Expected: FAIL to compile — `undefined: RepoMetaKey`, `undefined: SaveRepoMeta`, `unknown field FetchedAt`.

- [ ] **Step 3: Implement**

In `internal/core/repo_meta.go`, add the field to `RepoMeta`:

```go
type RepoMeta struct {
	Description   string   `json:"description,omitempty"`
	Topics        []string `json:"topics,omitempty"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	RemoteURL     string   `json:"remote_url,omitempty"`
	FetchedAt     int64    `json:"fetched_at,omitempty"` // Unix seconds of the last refresh
}
```

Add both functions below `MergeRepoMeta`:

```go
// RepoMetaKey returns the repo-meta.json key for a repo path: its shortest
// non-escaping path relative to any of roots, falling back to the path itself
// when no root matches. Writers use it so their keys match what MergeRepoMeta
// resolves on read.
func RepoMetaKey(roots []string, repoPath string) string {
	return bestRelUnder(roots, repoPath)
}

// SaveRepoMeta atomically writes the metadata cache. A nil map writes an empty
// object rather than "null", keeping the file loadable.
func SaveRepoMeta(path string, meta map[string]RepoMeta) error {
	if meta == nil {
		meta = map[string]RepoMeta{}
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}
```

Add `"github.com/freaxnx01/bridge/internal/store"` to the file's import block (`encoding/json` is already imported).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/ -run 'RepoMetaKey|SaveRepoMeta' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/core/
git commit -m "feat(core): add RepoMetaKey and SaveRepoMeta with fetched_at

The read half of repo-meta.json existed without a write half, and the
entry key was resolvable only privately.

Refs #297"
```

---

### Task 2: `remote` builds the path-keyed metadata map

The pure mapping function, tested on its own before anything writes a file.

**Files:**
- Modify: `internal/remote/remote.go` (add `buildRepoMeta` and `refIdentity`)
- Test: `internal/remote/remote_test.go`

**Interfaces:**
- Consumes: `core.RepoMetaKey`, `core.RepoMeta`, `core.DiscoverRepos`, `core.Repo` (Task 1).
- Produces: `func buildRepoMeta(roots []string, refs []forge.RepoRef, existing map[string]core.RepoMeta, now time.Time) map[string]core.RepoMeta` — one entry per local clone found under roots; entries with a matching ref are refreshed, entries without one keep their `existing` value, and repos no longer on disk are absent.

- [ ] **Step 1: Write the failing tests**

Append to `internal/remote/remote_test.go`:

```go
// mustMkRepo creates a git-looking repo dir under root so DiscoverRepos finds it.
func mustMkRepo(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Join(p, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildRepoMeta_MatchesRefsToClonesCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "github/acme/public/bridge")
	now := time.Unix(1789000000, 0)

	got := buildRepoMeta([]string{root}, []forge.RepoRef{{
		Forge: "github", Owner: "ACME", Name: "Bridge",
		Description: "repo navigator", Topics: []string{"go"},
		DefaultBranch: "main", SSHURL: "git@github.com:acme/bridge.git",
	}}, nil, now)

	entry, ok := got["github/acme/public/bridge"]
	if !ok {
		t.Fatalf("no entry for the clone, got keys %+v", got)
	}
	if entry.Description != "repo navigator" || entry.DefaultBranch != "main" {
		t.Errorf("entry not populated from the ref: %+v", entry)
	}
	if entry.RemoteURL != "git@github.com:acme/bridge.git" {
		t.Errorf("remote_url should come from the ref SSH URL: %+v", entry)
	}
	if entry.FetchedAt != now.Unix() {
		t.Errorf("FetchedAt = %d, want %d", entry.FetchedAt, now.Unix())
	}
}

func TestBuildRepoMeta_UnmatchedCloneKeepsExistingEntry(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "github/acme/public/bridge")
	existing := map[string]core.RepoMeta{
		"github/acme/public/bridge": {Description: "from a healthier day", FetchedAt: 1},
	}
	// No refs at all — e.g. the token 401'd this round.
	got := buildRepoMeta([]string{root}, nil, existing, time.Unix(1789000000, 0))

	entry, ok := got["github/acme/public/bridge"]
	if !ok {
		t.Fatalf("a failed forge must not drop the entry, got %+v", got)
	}
	if entry.Description != "from a healthier day" || entry.FetchedAt != 1 {
		t.Errorf("stale entry must be preserved verbatim: %+v", entry)
	}
}

func TestBuildRepoMeta_DropsEntriesForReposNoLongerOnDisk(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "github/acme/public/bridge")
	existing := map[string]core.RepoMeta{
		"github/acme/public/bridge": {Description: "still here"},
		"github/acme/public/deleted": {Description: "clone is gone"},
	}
	got := buildRepoMeta([]string{root}, nil, existing, time.Unix(1789000000, 0))

	if _, ok := got["github/acme/public/deleted"]; ok {
		t.Errorf("entry for a vanished clone must be dropped: %+v", got)
	}
	if _, ok := got["github/acme/public/bridge"]; !ok {
		t.Errorf("entry for a present clone must survive: %+v", got)
	}
}
```

Ensure the test file imports `"os"`, `"time"`, and `"github.com/freaxnx01/bridge/internal/core"` (it already imports `"path/filepath"`, `"testing"` and `"github.com/freaxnx01/bridge/internal/forge"`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/remote/ -run TestBuildRepoMeta -v`
Expected: FAIL to compile — `undefined: buildRepoMeta`.

- [ ] **Step 3: Implement**

In `internal/remote/remote.go`, add:

```go
// refIdentity is the case-insensitive forge+owner+name identity used to pair a
// fetched ref with a local clone. The separator cannot occur in any part.
func refIdentity(forgeName, owner, name string) string {
	return strings.ToLower(forgeName + "\x00" + owner + "\x00" + name)
}

// buildRepoMeta pairs the fetched refs with the clones under roots and returns
// the repo-meta.json map, keyed by each clone's root-relative path.
//
// A clone with no matching ref keeps its entry from existing, so a forge that
// failed this round loses nothing; an entry whose clone is gone from disk is
// dropped, so the file cannot grow without bound.
func buildRepoMeta(roots []string, refs []forge.RepoRef, existing map[string]core.RepoMeta, now time.Time) map[string]core.RepoMeta {
	byIdentity := make(map[string]forge.RepoRef, len(refs))
	for _, r := range refs {
		byIdentity[refIdentity(r.Forge, r.Owner, r.Name)] = r
	}
	out := map[string]core.RepoMeta{}
	for _, root := range roots {
		repos, err := core.DiscoverRepos(root)
		if err != nil {
			continue
		}
		for _, repo := range repos {
			key := core.RepoMetaKey(roots, repo.Path)
			if _, done := out[key]; done {
				continue
			}
			ref, ok := byIdentity[refIdentity(repo.Forge, repo.Owner, repo.Name)]
			if !ok {
				if prev, had := existing[key]; had {
					out[key] = prev
				}
				continue
			}
			out[key] = core.RepoMeta{
				Description:   ref.Description,
				Topics:        ref.Topics,
				DefaultBranch: ref.DefaultBranch,
				RemoteURL:     ref.SSHURL,
				FetchedAt:     now.Unix(),
			}
		}
	}
	return out
}
```

Add `"strings"` and `"github.com/freaxnx01/bridge/internal/core"` to the file's imports (`time` is already imported).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/remote/ -run TestBuildRepoMeta -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/remote/
git commit -m "feat(remote): build the repo-meta map from refs and local clones

Refs #297"
```

---

### Task 3: `Refresh` writes `repo-meta.json` beside `remote.list`

The wiring task. `Refresh` gains the meta path as a parameter so path policy stays in `cmd/bridge`, and both call sites pass it.

**Files:**
- Modify: `internal/remote/remote.go:29-57` (`Refresh`)
- Modify: `cmd/bridge/list.go:90-98` (`loadOrFetchRemote`)
- Modify: `cmd/bridge/nav.go:66-68` (`FetchRemote` callback)
- Test: `internal/remote/remote_test.go` (`TestRefresh_NoToken_WritesCacheNoNetwork` call updated, new tests added)

**Interfaces:**
- Consumes: `buildRepoMeta` (Task 2), `core.LoadRepoMeta` / `core.SaveRepoMeta` (Task 1).
- Produces: `func Refresh(ctx context.Context, roots []string, cachePath, metaPath string) ([]forge.RepoRef, error)` — unchanged return values; now also writes `metaPath`, best-effort.

- [ ] **Step 1: Write the failing tests**

In `internal/remote/remote_test.go`, update the existing call in
`TestRefresh_NoToken_WritesCacheNoNetwork` — it takes a fourth argument now:

```go
	metaPath := filepath.Join(t.TempDir(), "repo-meta.json")
	repos, err := Refresh(context.Background(), []string{root}, cachePath, metaPath)
```

Then append:

```go
func TestRefresh_WritesRepoMetaBesideRemoteList(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	mustMkRepo(t, root, "github/acme/public/bridge")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	metaPath := filepath.Join(t.TempDir(), "repo-meta.json")

	if _, err := Refresh(context.Background(), []string{root}, cachePath, metaPath); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// No token, so no refs — but the file must still be written, with an entry
	// absent rather than the file missing.
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("repo-meta.json not written: %v", err)
	}
	if _, err := core.LoadRepoMeta(metaPath); err != nil {
		t.Errorf("written repo-meta.json is not loadable: %v", err)
	}
}

func TestRefresh_MetaWriteFailure_DoesNotFailTheRefresh(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	// A directory where the file should go: every write to it fails.
	metaDir := t.TempDir()
	metaPath := filepath.Join(metaDir, "repo-meta.json")
	if err := os.MkdirAll(metaPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(context.Background(), []string{root}, cachePath, metaPath); err != nil {
		t.Errorf("a failed meta write must not fail Refresh, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/remote/ -run TestRefresh -v`
Expected: FAIL to compile — `too many arguments in call to Refresh`.

- [ ] **Step 3: Implement**

In `internal/remote/remote.go`, change the signature and the doc comment, and add the write. The function becomes:

```go
// Refresh discovers every forge target reachable from roots, fetches each
// owner's repos, writes the merged result to cachePath and the per-clone
// metadata to metaPath, and returns the refs. The first per-target error is
// returned alongside whatever repos did succeed, so a single failing forge does
// not lose the others.
func Refresh(ctx context.Context, roots []string, cachePath, metaPath string) ([]forge.RepoRef, error) {
```

The body is unchanged down to the cache write, which gains a sibling:

```go
	// Best-effort cache writes: callers already have the fresh repos in `all`;
	// a write failure must not fail the refresh.
	_ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
	existing, _ := core.LoadRepoMeta(metaPath)
	_ = core.SaveRepoMeta(metaPath, buildRepoMeta(roots, all, existing, time.Now()))
	return all, firstErr
}
```

In `cmd/bridge/list.go`, `loadOrFetchRemote`'s last line becomes:

```go
	return remote.Refresh(ctx, reposRoots(), cachePath, filepath.Join(cacheRoot(), "repo-meta.json"))
```

In `cmd/bridge/nav.go`, the `FetchRemote` callback becomes:

```go
			FetchRemote: func(ctx context.Context) ([]forge.RepoRef, error) {
				return remote.Refresh(ctx, reposRoots(),
					filepath.Join(cacheRoot(), "remote.list"),
					filepath.Join(cacheRoot(), "repo-meta.json"))
			},
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/remote/ ./cmd/bridge/ -run 'Refresh|List' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Verify `core` still does not import `forge`**

Run: `go list -deps ./internal/core | grep 'bridge/internal/forge' && echo "VIOLATION" || echo "clean"`
Expected: `clean`. A `VIOLATION` means the map got built in the wrong package — move it to `internal/remote`.

- [ ] **Step 7: Commit**

```bash
git add internal/remote/ cmd/bridge/
git commit -m "feat(remote): write repo-meta.json on every remote refresh

Covers list -r --refresh, bridge --refresh and nav's r/ctrl+r in one
place, since all three funnel through Refresh.

Refs #297"
```

---

### Task 4: Document it

**Files:**
- Modify: `CHANGELOG.md` (the `## [Unreleased]` section)
- Modify: `README.md:75` (the cache table row)

**Interfaces:**
- Consumes: the finished behaviour from Tasks 1-3.
- Produces: nothing code-facing.

- [ ] **Step 1: Add the changelog entry**

Under `## [Unreleased]` → `### Fixed` in `CHANGELOG.md` (create the `### Fixed` section if it is not there yet, after `### Changed`):

```markdown
- `repo-meta.json` is **written again**. The README documented `list -r [--refresh]` as its writer, but no Go code wrote the file — it had been stale since the bash implementation, while `bridge open <keyword>` and `bridge nextgen<TAB>` kept reading it for description/topic search. Every remote refresh (`list -r --refresh`, `bridge --refresh`, nav's `r`/`ctrl+r`) now writes it, keyed by each clone's root-relative path. A repo whose forge fails that round keeps its previous entry rather than losing it, and entries for clones that are gone from disk are dropped. Entries carry `fetched_at`. (#297)
```

- [ ] **Step 2: Correct the README cache table**

In `README.md:75`, the row currently reads:

```markdown
| `repo-meta.json` | `list -r [--refresh]` | per-repo topics/description/default-branch/remote URL |
```

Replace it with a row naming every writer, so the table stops under-reporting:

```markdown
| `repo-meta.json` | `list -r [--refresh]`, `bridge --refresh`, `nav` (`r`/`^r`) | per-repo topics/description/default-branch/remote URL, plus `fetched_at` |
```

- [ ] **Step 3: Verify the docs lint clean**

Run: `npx --yes markdownlint-cli2 CHANGELOG.md README.md`
Expected: no errors. If `markdownlint-cli2` is unavailable offline, skip it and visually confirm the table and list formatting match the surrounding rows.

- [ ] **Step 4: Run the full verification set**

Run:

```bash
gofmt -l .
go vet ./...
golangci-lint run
go test -race ./...
```

Expected: `gofmt -l .` prints nothing; the rest clean/PASS.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "docs: record the restored repo-meta.json writer

Closes #297"
```

---

## Manual verification (after Task 4)

The unit tests cover the mapping and the writes; this checks it against the real cache:

```bash
just build
stat -c '%y' ~/.cache/bridge/repo-meta.json    # before: old
bridge list -r --refresh >/dev/null
stat -c '%y' ~/.cache/bridge/repo-meta.json    # after: now
python3 -c "import json;d=json.load(open('$HOME/.cache/bridge/repo-meta.json'));k=[x for x in d if x.endswith('/bridge')][0];print(k, d[k])"
```

Expect the `bridge` entry to carry a non-empty `description`, a `default_branch`, a `remote_url`, and a `fetched_at` of roughly now. Then:

```bash
bridge nextgen<TAB>
```

Expect it to complete to `ArchiveRestApiNextGen` via the description/topic match (`README.md:202`), which is the user-visible point of the cache.
