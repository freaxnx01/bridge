# bridge core redesign — Plan A (Phases 0+1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Go binary `bridge-go` that implements all read-only subcommands of the redesigned CLI (`list`, `sessions`, `slots`, `presence`, `sync`, `status`, `issues`, `--version`) alongside the existing bash `bridge`. Foundation only — no interactive paths, no launcher, no shim, no cutover. Those land in Plan B.

**Architecture:** Single Go binary, stateless per invocation, reads/writes `~/.cache/bridge/`. Cobra for CLI. `internal/core` owns domain types, `internal/store` owns file IO with atomic writes and flock, `internal/forge` talks to GitHub/GitLab/Forgejo with TTL'd caches. Each subcommand supports `--json`. Tests are unit (table-driven) for packages, golden-file integration for `cmd/bridge` via `exec.Command`, and httptest mock servers for forge clients.

**Tech Stack:** Go 1.22+, `github.com/spf13/cobra`, `log/slog` (stdlib), `net/http/httptest` (stdlib). No external deps beyond cobra for now.

**Spec:** `docs/specs/2026-05-25-bridge-core-redesign-design.md`

---

## File Structure

Files this plan creates or modifies:

```
go.mod                              # root module (separate from prototypes/dashboard-tui)
go.sum
.github/workflows/go.yml            # build + test + cross-compile check
Makefile                            # add `make build-go` target
.gitignore                          # add /bridge-go binary

cmd/bridge/
  main.go                           # entrypoint
  root.go                           # cobra root + global flags + version info
  output.go                         # shared --json / human rendering helpers
  list.go                           # bridge list
  slots.go                          # bridge slots
  sessions.go                       # bridge sessions
  presence.go                       # bridge presence (read only)
  sync.go                           # bridge sync (status only)
  status.go                         # bridge status (slim composed)
  issues.go                         # bridge issues

internal/core/
  repo.go                           # Repo type + filesystem discovery
  repo_test.go
  mru.go                            # MRU read
  mru_test.go
  slot.go                           # Slot type + read
  slot_test.go
  session.go                        # Session inspection (tmux read)
  session_test.go
  presence.go                       # Presence type + read
  presence_test.go
  issue.go                          # Issue type

internal/store/
  store.go                          # paths, schema-version
  files.go                          # atomic read/write
  files_test.go
  lock.go                           # flock abstraction
  lock_test.go
  schema.go                         # versioning + backup
  schema_test.go

internal/forge/
  client.go                         # Client interface, Repo, Issue (forge-level types)
  cache.go                          # TTL cache (repo listings, issues)
  cache_test.go
  github.go
  github_test.go
  gitlab.go
  gitlab_test.go
  forgejo.go
  forgejo_test.go

cmd/bridge/testdata/                # golden files
  list/...
  slots/...
  sessions/...
  presence/...
  sync/...
  issues/...
  status/...

docs/cli-json-schema.md             # initial JSON schema docs (grows in B)
```

`bridge.sh` and other existing bash scripts are NOT modified in this plan.

---

## Task 1: Bootstrap Go module + CI skeleton ✅

**Files:**
- Create: `go.mod`
- Create: `cmd/bridge/main.go`
- Create: `.github/workflows/go.yml`
- Modify: `.gitignore`
- Modify: `Makefile`

- [ ] **Step 1: Initialize the Go module**

Run:
```bash
go mod init github.com/freaxnx01/bridge
```

Expected: `go.mod created` and a `go.mod` file at repo root.

- [ ] **Step 2: Create the minimal `cmd/bridge/main.go`**

Create `cmd/bridge/main.go`:
```go
package main

import "fmt"

func main() {
    fmt.Println("bridge-go (skeleton)")
}
```

- [ ] **Step 3: Verify it builds**

Run:
```bash
go build -o bridge-go ./cmd/bridge && ./bridge-go
```

Expected output: `bridge-go (skeleton)`

- [ ] **Step 4: Update `.gitignore`**

Append to `.gitignore`:
```
/bridge-go
/bridge-go.exe
```

- [ ] **Step 5: Add `make build-go` target**

Append to `Makefile`:
```make
.PHONY: build-go test-go

build-go:
	go build -o bridge-go ./cmd/bridge

test-go:
	go test ./...
```

Run `make build-go` and confirm `./bridge-go` is produced.

- [ ] **Step 6: Add CI workflow**

Create `.github/workflows/go.yml`:
```yaml
name: go

on:
  push:
    branches: [main]
  pull_request:

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: build
        run: go build ./...
      - name: test
        run: go test ./...
      - name: cross-compile windows
        env:
          GOOS: windows
          GOARCH: amd64
        run: go build -o /tmp/bridge.exe ./cmd/bridge
```

- [ ] **Step 7: Commit**

```bash
git add go.mod cmd/bridge/main.go .github/workflows/go.yml .gitignore Makefile
git commit -m "feat(go): bootstrap Go module and CI skeleton"
```

---

## Task 2: Cobra root command + `bridge --version` ✅

**Files:**
- Modify: `go.mod` (add cobra)
- Modify: `cmd/bridge/main.go`
- Create: `cmd/bridge/root.go`
- Create: `cmd/bridge/version_test.go`

- [ ] **Step 1: Add cobra dependency**

Run:
```bash
go get github.com/spf13/cobra@latest
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `cmd/bridge/version_test.go`:
```go
package main

import (
    "os/exec"
    "strings"
    "testing"
)

func TestVersionCommand(t *testing.T) {
    out, err := exec.Command("go", "run", ".", "--version").CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    s := string(out)
    if !strings.Contains(s, "bridge") {
        t.Errorf("expected 'bridge' in output, got: %s", s)
    }
}
```

- [ ] **Step 3: Run the test and confirm it fails**

Run:
```bash
go test ./cmd/bridge -run TestVersionCommand -v
```

Expected: FAIL (unknown flag `--version` or non-cobra output).

- [ ] **Step 4: Implement cobra root**

Replace `cmd/bridge/main.go`:
```go
package main

import (
    "fmt"
    "os"
)

func main() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

Create `cmd/bridge/root.go`:
```go
package main

import "github.com/spf13/cobra"

var (
    version = "dev"
    commit  = "none"
    date    = "unknown"
)

var rootCmd = &cobra.Command{
    Use:     "bridge",
    Short:   "Repo picker + agent launcher (Go core)",
    Version: versionString(),
}

func versionString() string {
    return "bridge " + version + " (commit " + commit + ", built " + date + ")"
}
```

- [ ] **Step 5: Run the test and confirm it passes**

Run:
```bash
go test ./cmd/bridge -run TestVersionCommand -v
```

Expected: PASS.

- [ ] **Step 6: Wire ldflags in Makefile so `version` is set at build time**

Edit `Makefile` `build-go` target:
```make
build-go:
	go build \
		-ldflags "-X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o bridge-go ./cmd/bridge
```

Run `make build-go && ./bridge-go --version` and confirm a real version string with a commit SHA.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum cmd/bridge/main.go cmd/bridge/root.go cmd/bridge/version_test.go Makefile
git commit -m "feat(go): cobra root command with --version"
```

---

## Task 3: `internal/store` — atomic file IO ✅

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/files.go`
- Create: `internal/store/files_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/files_test.go`:
```go
package store

import (
    "os"
    "path/filepath"
    "testing"
)

func TestAtomicWriteCreatesFile(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "x.json")
    if err := AtomicWrite(p, []byte(`{"a":1}`)); err != nil {
        t.Fatalf("write: %v", err)
    }
    b, err := os.ReadFile(p)
    if err != nil {
        t.Fatalf("read: %v", err)
    }
    if string(b) != `{"a":1}` {
        t.Errorf("got %q", b)
    }
}

func TestAtomicWriteReplaces(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "x.json")
    _ = os.WriteFile(p, []byte("old"), 0o644)
    if err := AtomicWrite(p, []byte("new")); err != nil {
        t.Fatal(err)
    }
    b, _ := os.ReadFile(p)
    if string(b) != "new" {
        t.Errorf("got %q", b)
    }
}

func TestReadFileMissingIsEmpty(t *testing.T) {
    b, err := ReadFile(filepath.Join(t.TempDir(), "missing"))
    if err != nil {
        t.Fatalf("err: %v", err)
    }
    if len(b) != 0 {
        t.Errorf("expected empty, got %q", b)
    }
}

func TestReadFileExisting(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "x")
    _ = os.WriteFile(p, []byte("hi"), 0o644)
    b, err := ReadFile(p)
    if err != nil {
        t.Fatal(err)
    }
    if string(b) != "hi" {
        t.Errorf("got %q", b)
    }
}

func TestAtomicWriteCreatesParentDirs(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "sub", "nested", "x.json")
    if err := AtomicWrite(p, []byte("v")); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(p); err != nil {
        t.Fatalf("expected file: %v", err)
    }
}
```

- [ ] **Step 2: Run the tests; confirm they fail**

Run:
```bash
go test ./internal/store -v
```

Expected: FAIL (`AtomicWrite`, `ReadFile` undefined).

- [ ] **Step 3: Implement `store.go` (paths)**

Create `internal/store/store.go`:
```go
package store

import (
    "os"
    "path/filepath"
)

// Dir returns the bridge cache directory (~/.cache/bridge).
func Dir() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil {
        return "", err
    }
    return filepath.Join(home, ".cache", "bridge"), nil
}

// Path joins a name onto the cache dir.
func Path(name string) (string, error) {
    d, err := Dir()
    if err != nil {
        return "", err
    }
    return filepath.Join(d, name), nil
}
```

- [ ] **Step 4: Implement `files.go`**

Create `internal/store/files.go`:
```go
package store

import (
    "errors"
    "os"
    "path/filepath"
)

// AtomicWrite writes data to path via a tmp-file + rename in the same directory.
// Creates parent directories with mode 0o755 if needed.
func AtomicWrite(path string, data []byte) error {
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return err
    }
    f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
    if err != nil {
        return err
    }
    tmp := f.Name()
    if _, err := f.Write(data); err != nil {
        f.Close()
        os.Remove(tmp)
        return err
    }
    if err := f.Sync(); err != nil {
        f.Close()
        os.Remove(tmp)
        return err
    }
    if err := f.Close(); err != nil {
        os.Remove(tmp)
        return err
    }
    return os.Rename(tmp, path)
}

// ReadFile reads path. Returns empty bytes and nil error if the file is missing.
func ReadFile(path string) ([]byte, error) {
    b, err := os.ReadFile(path)
    if errors.Is(err, os.ErrNotExist) {
        return nil, nil
    }
    return b, err
}
```

- [ ] **Step 5: Run the tests; confirm they pass**

Run:
```bash
go test ./internal/store -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/files.go internal/store/files_test.go
git commit -m "feat(go): internal/store atomic file IO"
```

---

## Task 4: `internal/store` — flock abstraction ✅

**Files:**
- Create: `internal/store/lock.go`
- Create: `internal/store/lock_unix.go`
- Create: `internal/store/lock_windows.go`
- Create: `internal/store/lock_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/lock_test.go`:
```go
package store

import (
    "path/filepath"
    "sync"
    "testing"
    "time"
)

func TestLockExcludesConcurrent(t *testing.T) {
    p := filepath.Join(t.TempDir(), "lock")

    var holdMu sync.Mutex
    held := false

    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        l, err := AcquireLock(p)
        if err != nil {
            t.Errorf("first acquire: %v", err)
            return
        }
        holdMu.Lock()
        held = true
        holdMu.Unlock()
        time.Sleep(200 * time.Millisecond)
        holdMu.Lock()
        held = false
        holdMu.Unlock()
        _ = l.Release()
    }()

    time.Sleep(50 * time.Millisecond)

    l2, err := AcquireLock(p)
    if err != nil {
        t.Fatalf("second acquire: %v", err)
    }
    holdMu.Lock()
    if held {
        t.Error("second acquire returned while first still held lock")
    }
    holdMu.Unlock()
    _ = l2.Release()
    wg.Wait()
}
```

- [ ] **Step 2: Run the test; confirm it fails**

Run `go test ./internal/store -run TestLockExcludesConcurrent -v`. Expected: FAIL (`AcquireLock` undefined).

- [ ] **Step 3: Define the cross-platform interface**

Create `internal/store/lock.go`:
```go
package store

// Lock is a held file lock. Call Release exactly once.
type Lock interface {
    Release() error
}
```

- [ ] **Step 4: Implement Unix flock**

Create `internal/store/lock_unix.go`:
```go
//go:build !windows

package store

import (
    "os"
    "path/filepath"

    "golang.org/x/sys/unix"
)

type unixLock struct {
    f *os.File
}

func (l *unixLock) Release() error {
    err := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
    cerr := l.f.Close()
    if err != nil {
        return err
    }
    return cerr
}

func AcquireLock(path string) (Lock, error) {
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return nil, err
    }
    f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
    if err != nil {
        return nil, err
    }
    if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
        f.Close()
        return nil, err
    }
    return &unixLock{f: f}, nil
}
```

Run:
```bash
go get golang.org/x/sys/unix
go mod tidy
```

- [ ] **Step 5: Implement Windows lock**

Create `internal/store/lock_windows.go`:
```go
//go:build windows

package store

import (
    "os"
    "path/filepath"

    "golang.org/x/sys/windows"
)

type winLock struct {
    f *os.File
}

func (l *winLock) Release() error {
    h := windows.Handle(l.f.Fd())
    var ol windows.Overlapped
    err := windows.UnlockFileEx(h, 0, ^uint32(0), ^uint32(0), &ol)
    cerr := l.f.Close()
    if err != nil {
        return err
    }
    return cerr
}

func AcquireLock(path string) (Lock, error) {
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return nil, err
    }
    f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
    if err != nil {
        return nil, err
    }
    h := windows.Handle(f.Fd())
    var ol windows.Overlapped
    if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), &ol); err != nil {
        f.Close()
        return nil, err
    }
    return &winLock{f: f}, nil
}
```

- [ ] **Step 6: Run the test; confirm it passes**

Run `go test ./internal/store -v`. Expected: PASS.

- [ ] **Step 7: Confirm Windows cross-compile still works**

Run:
```bash
GOOS=windows GOARCH=amd64 go build -o /tmp/bridge.exe ./cmd/bridge
```

Expected: clean build.

- [ ] **Step 8: Commit**

```bash
git add internal/store/lock.go internal/store/lock_unix.go internal/store/lock_windows.go internal/store/lock_test.go go.mod go.sum
git commit -m "feat(go): flock abstraction (Unix + Windows)"
```

---

## Task 5: `internal/store` — schema versioning ✅

**Files:**
- Create: `internal/store/schema.go`
- Create: `internal/store/schema_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/schema_test.go`:
```go
package store

import (
    "os"
    "path/filepath"
    "testing"
)

func TestReadSchemaMissingIsZero(t *testing.T) {
    dir := t.TempDir()
    v, err := ReadSchemaVersionFrom(dir)
    if err != nil {
        t.Fatal(err)
    }
    if v != 0 {
        t.Errorf("got %d", v)
    }
}

func TestWriteAndReadSchema(t *testing.T) {
    dir := t.TempDir()
    if err := WriteSchemaVersionTo(dir, 3); err != nil {
        t.Fatal(err)
    }
    v, err := ReadSchemaVersionFrom(dir)
    if err != nil {
        t.Fatal(err)
    }
    if v != 3 {
        t.Errorf("got %d", v)
    }
}

func TestBackupBeforeMigrate(t *testing.T) {
    dir := t.TempDir()
    src := filepath.Join(dir, "presence.json")
    _ = os.WriteFile(src, []byte("old"), 0o644)
    if err := BackupForMigrate(src, 2); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(filepath.Join(dir, "presence.json.bak-2")); err != nil {
        t.Errorf("expected backup file: %v", err)
    }
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/store -v -run TestReadSchema` — FAIL.

- [ ] **Step 3: Implement**

Create `internal/store/schema.go`:
```go
package store

import (
    "fmt"
    "path/filepath"
    "strconv"
    "strings"
)

// CurrentSchema is the schema version this binary writes.
const CurrentSchema = 1

// ReadSchemaVersion reads the schema-version file from the default cache dir.
func ReadSchemaVersion() (int, error) {
    d, err := Dir()
    if err != nil {
        return 0, err
    }
    return ReadSchemaVersionFrom(d)
}

// ReadSchemaVersionFrom reads schema-version from dir. Missing == 0.
func ReadSchemaVersionFrom(dir string) (int, error) {
    b, err := ReadFile(filepath.Join(dir, "schema-version"))
    if err != nil {
        return 0, err
    }
    s := strings.TrimSpace(string(b))
    if s == "" {
        return 0, nil
    }
    return strconv.Atoi(s)
}

// WriteSchemaVersion writes v to the default cache dir.
func WriteSchemaVersion(v int) error {
    d, err := Dir()
    if err != nil {
        return err
    }
    return WriteSchemaVersionTo(d, v)
}

// WriteSchemaVersionTo writes v to dir.
func WriteSchemaVersionTo(dir string, v int) error {
    return AtomicWrite(filepath.Join(dir, "schema-version"), []byte(strconv.Itoa(v)))
}

// BackupForMigrate copies src to src.bak-<fromVersion> before in-place migration.
func BackupForMigrate(src string, fromVersion int) error {
    b, err := ReadFile(src)
    if err != nil {
        return err
    }
    if len(b) == 0 {
        return nil
    }
    return AtomicWrite(fmt.Sprintf("%s.bak-%d", src, fromVersion), b)
}
```

- [ ] **Step 4: Run; expect pass**

`go test ./internal/store -v`. PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/schema.go internal/store/schema_test.go
git commit -m "feat(go): internal/store schema versioning + migration backup"
```

---

## Task 6: `internal/core` — Repo type + filesystem discovery ✅

**Files:**
- Create: `internal/core/repo.go`
- Create: `internal/core/repo_test.go`

The discovery contract from the spec: walk `~/projects/repos/` looking for `.envrc` files. Path patterns determine forge/owner/visibility.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/repo_test.go`:
```go
package core

import (
    "os"
    "path/filepath"
    "sort"
    "testing"
    "time"
)

func setupFakeRepos(t *testing.T) string {
    t.Helper()
    root := t.TempDir()
    layout := []string{
        "github/freaxnx01/public/bridge",
        "github/freaxnx01/private/secret-thing",
        "github/otheruser/public/lib",
        "gitlab/freaxnx01/some-gl-repo",
        "git-forgejo/forgejo-repo",
    }
    for _, p := range layout {
        full := filepath.Join(root, p)
        if err := os.MkdirAll(full, 0o755); err != nil {
            t.Fatal(err)
        }
    }
    // Put .envrc at the right "credential boundary" per the spec:
    // - github/<owner>/<vis>/.envrc applies to each repo under it
    // - gitlab/<owner>/.envrc applies to each repo under it
    // - git-forgejo/.envrc applies to each repo under it
    envrcs := []string{
        "github/freaxnx01/public/.envrc",
        "github/freaxnx01/private/.envrc",
        "github/otheruser/public/.envrc",
        "gitlab/freaxnx01/.envrc",
        "git-forgejo/.envrc",
    }
    for _, p := range envrcs {
        if err := os.WriteFile(filepath.Join(root, p), []byte("export TOKEN=x"), 0o644); err != nil {
            t.Fatal(err)
        }
    }
    return root
}

func TestDiscoverRepos(t *testing.T) {
    root := setupFakeRepos(t)
    repos, err := DiscoverRepos(root)
    if err != nil {
        t.Fatal(err)
    }
    sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })

    if len(repos) != 5 {
        t.Fatalf("want 5 repos, got %d: %+v", len(repos), repos)
    }

    want := []struct {
        name, forge, owner, vis string
    }{
        {"forgejo-repo", "forgejo", "freax", ""},
        {"some-gl-repo", "gitlab", "freaxnx01", ""},
        {"lib", "github", "otheruser", "public"},
        {"secret-thing", "github", "freaxnx01", "private"},
        {"bridge", "github", "freaxnx01", "public"},
    }
    sort.Slice(want, func(i, j int) bool { return want[i].name < want[j].name })
    sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })

    for i, w := range want {
        if repos[i].Name != w.name || repos[i].Forge != w.forge ||
            repos[i].Owner != w.owner || repos[i].Visibility != w.vis {
            t.Errorf("[%d] got %+v, want %+v", i, repos[i], w)
        }
    }
}

func TestRepoTypeZeroLastUsed(t *testing.T) {
    var r Repo
    if !r.LastUsed.Equal(time.Time{}) {
        t.Errorf("zero LastUsed expected")
    }
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/core -v` — FAIL (`Repo`, `DiscoverRepos` undefined).

- [ ] **Step 3: Implement `repo.go`**

Create `internal/core/repo.go`:
```go
package core

import (
    "os"
    "path/filepath"
    "strings"
    "time"
)

// Repo is a discovered local repository.
type Repo struct {
    Name          string    `json:"name"`
    Path          string    `json:"path"`
    Forge         string    `json:"forge"`         // "github" | "gitlab" | "forgejo"
    Owner         string    `json:"owner"`
    Visibility    string    `json:"visibility"`    // "public" | "private" | ""
    Topics        []string  `json:"topics,omitempty"`
    Desc          string    `json:"desc,omitempty"`
    DefaultBranch string    `json:"default_branch,omitempty"`
    RemoteURL     string    `json:"remote_url,omitempty"`
    LastUsed      time.Time `json:"last_used,omitempty"`
}

// DiscoverRepos walks root (typically ~/projects/repos) and returns repos
// according to the project's layout patterns:
//   github/<owner>/(public|private)/<repo>
//   gitlab/<owner>/<repo>
//   git-forgejo/<repo>
func DiscoverRepos(root string) ([]Repo, error) {
    var out []Repo
    walkGithub := func(forgeDir string) error {
        owners, err := os.ReadDir(forgeDir)
        if err != nil {
            return err
        }
        for _, owner := range owners {
            if !owner.IsDir() {
                continue
            }
            for _, vis := range []string{"public", "private"} {
                visDir := filepath.Join(forgeDir, owner.Name(), vis)
                repos, err := os.ReadDir(visDir)
                if err != nil {
                    continue
                }
                for _, r := range repos {
                    if !r.IsDir() {
                        continue
                    }
                    out = append(out, Repo{
                        Name:       r.Name(),
                        Path:       filepath.Join(visDir, r.Name()),
                        Forge:      "github",
                        Owner:      owner.Name(),
                        Visibility: vis,
                    })
                }
            }
        }
        return nil
    }
    walkGitlab := func(forgeDir string) error {
        owners, err := os.ReadDir(forgeDir)
        if err != nil {
            return err
        }
        for _, owner := range owners {
            if !owner.IsDir() {
                continue
            }
            ownerDir := filepath.Join(forgeDir, owner.Name())
            repos, err := os.ReadDir(ownerDir)
            if err != nil {
                continue
            }
            for _, r := range repos {
                if !r.IsDir() {
                    continue
                }
                if strings.HasPrefix(r.Name(), ".") {
                    continue
                }
                out = append(out, Repo{
                    Name:  r.Name(),
                    Path:  filepath.Join(ownerDir, r.Name()),
                    Forge: "gitlab",
                    Owner: owner.Name(),
                })
            }
        }
        return nil
    }
    walkForgejo := func(forgeDir string) error {
        repos, err := os.ReadDir(forgeDir)
        if err != nil {
            return err
        }
        for _, r := range repos {
            if !r.IsDir() {
                continue
            }
            if strings.HasPrefix(r.Name(), ".") {
                continue
            }
            out = append(out, Repo{
                Name:  r.Name(),
                Path:  filepath.Join(forgeDir, r.Name()),
                Forge: "forgejo",
                Owner: "freax", // hardcoded per spec
            })
        }
        return nil
    }

    if d := filepath.Join(root, "github"); dirExists(d) {
        if err := walkGithub(d); err != nil {
            return nil, err
        }
    }
    if d := filepath.Join(root, "gitlab"); dirExists(d) {
        if err := walkGitlab(d); err != nil {
            return nil, err
        }
    }
    if d := filepath.Join(root, "git-forgejo"); dirExists(d) {
        if err := walkForgejo(d); err != nil {
            return nil, err
        }
    }
    return out, nil
}

func dirExists(p string) bool {
    fi, err := os.Stat(p)
    return err == nil && fi.IsDir()
}
```

- [ ] **Step 4: Run; expect pass**

`go test ./internal/core -v`. PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/repo.go internal/core/repo_test.go
git commit -m "feat(go): core.Repo type + filesystem discovery"
```

---

## Task 7: `internal/core` — MRU read ✅

**Files:**
- Create: `internal/core/mru.go`
- Create: `internal/core/mru_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/core/mru_test.go`:
```go
package core

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadMRU(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "mru")
    _ = os.WriteFile(p, []byte("/a\n/b\n/a\n/c\n"), 0o644)
    paths, err := LoadMRU(p)
    if err != nil {
        t.Fatal(err)
    }
    // Most-recent first, dedupe keeping latest occurrence
    want := []string{"/c", "/a", "/b"}
    if len(paths) != len(want) {
        t.Fatalf("got %v", paths)
    }
    for i := range want {
        if paths[i] != want[i] {
            t.Errorf("[%d] got %s want %s", i, paths[i], want[i])
        }
    }
}

func TestLoadMRUMissing(t *testing.T) {
    paths, err := LoadMRU(filepath.Join(t.TempDir(), "missing"))
    if err != nil {
        t.Fatal(err)
    }
    if len(paths) != 0 {
        t.Errorf("want empty, got %v", paths)
    }
}

func TestLoadMRUSkipsBlank(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "mru")
    _ = os.WriteFile(p, []byte("/a\n\n  \n/b\n"), 0o644)
    paths, _ := LoadMRU(p)
    if len(paths) != 2 || paths[0] != "/b" || paths[1] != "/a" {
        t.Errorf("got %v", paths)
    }
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/core -run TestLoadMRU -v` — FAIL.

- [ ] **Step 3: Implement**

Create `internal/core/mru.go`:
```go
package core

import (
    "bufio"
    "errors"
    "os"
    "strings"
)

// LoadMRU reads a newline-delimited MRU file. Returns most-recent-first,
// deduped (keeping the latest occurrence). Missing file → empty slice.
func LoadMRU(path string) ([]string, error) {
    f, err := os.Open(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil, nil
        }
        return nil, err
    }
    defer f.Close()

    var raw []string
    sc := bufio.NewScanner(f)
    for sc.Scan() {
        line := strings.TrimSpace(sc.Text())
        if line == "" {
            continue
        }
        raw = append(raw, line)
    }
    if err := sc.Err(); err != nil {
        return nil, err
    }

    // Reverse and dedupe — keep the most-recent occurrence.
    seen := make(map[string]bool, len(raw))
    out := make([]string, 0, len(raw))
    for i := len(raw) - 1; i >= 0; i-- {
        if seen[raw[i]] {
            continue
        }
        seen[raw[i]] = true
        out = append(out, raw[i])
    }
    return out, nil
}
```

- [ ] **Step 4: Run; expect pass**

`go test ./internal/core -v`. PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/mru.go internal/core/mru_test.go
git commit -m "feat(go): core.LoadMRU"
```

---

## Task 8: `cmd/bridge list` — local repos with `--json` and human output ✅

**Files:**
- Create: `cmd/bridge/output.go`
- Create: `cmd/bridge/list.go`
- Create: `cmd/bridge/list_test.go`
- Create: `cmd/bridge/testdata/list/local_human.txt`
- Create: `cmd/bridge/testdata/list/local_json.txt`

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/list_test.go`:
```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func runBridge(t *testing.T, root string, args ...string) (string, string, int) {
    t.Helper()
    cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
    cmd.Env = append(os.Environ(), "BRIDGE_REPOS_ROOT="+root)
    var sout, serr stringBuf
    cmd.Stdout = &sout
    cmd.Stderr = &serr
    err := cmd.Run()
    code := 0
    if ee, ok := err.(*exec.ExitError); ok {
        code = ee.ExitCode()
    } else if err != nil {
        t.Fatalf("run: %v", err)
    }
    return sout.String(), serr.String(), code
}

type stringBuf struct{ b []byte }

func (s *stringBuf) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *stringBuf) String() string              { return string(s.b) }

func writeFakeRepos(t *testing.T) string {
    t.Helper()
    root := t.TempDir()
    for _, p := range []string{
        "github/freaxnx01/public/bridge",
        "github/freaxnx01/private/secret",
        "gitlab/freaxnx01/glrepo",
    } {
        if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
            t.Fatal(err)
        }
    }
    return root
}

func TestListLocalHuman(t *testing.T) {
    root := writeFakeRepos(t)
    out, _, code := runBridge(t, root, "list")
    if code != 0 {
        t.Fatalf("exit %d", code)
    }
    if !contains(out, "bridge") || !contains(out, "secret") || !contains(out, "glrepo") {
        t.Errorf("missing repo in output: %s", out)
    }
}

func TestListLocalJSON(t *testing.T) {
    root := writeFakeRepos(t)
    out, _, code := runBridge(t, root, "list", "--json")
    if code != 0 {
        t.Fatalf("exit %d", code)
    }
    var repos []map[string]any
    if err := json.Unmarshal([]byte(out), &repos); err != nil {
        t.Fatalf("json: %v in %s", err, out)
    }
    if len(repos) != 3 {
        t.Errorf("want 3, got %d", len(repos))
    }
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
    for i := 0; i+len(sub) <= len(s); i++ {
        if s[i:i+len(sub)] == sub {
            return i
        }
    }
    return -1
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./cmd/bridge -run TestListLocal -v` — FAIL (unknown command `list`).

- [ ] **Step 3: Implement output helpers**

Create `cmd/bridge/output.go`:
```go
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
)

// emitJSON writes v as JSON + newline to w.
func emitJSON(w io.Writer, v any) error {
    b, err := json.MarshalIndent(v, "", "  ")
    if err != nil {
        return err
    }
    _, err = fmt.Fprintln(w, string(b))
    return err
}

// emitJSONError writes a structured error to stderr.
func emitJSONError(msg string, code int) {
    type errOut struct {
        Error string `json:"error"`
        Code  int    `json:"code"`
    }
    b, _ := json.Marshal(errOut{Error: msg, Code: code})
    fmt.Fprintln(os.Stderr, string(b))
}
```

- [ ] **Step 4: Implement the `list` command**

Create `cmd/bridge/list.go`:
```go
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "sort"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
)

var (
    listJSON    bool
    listRemote  bool
    listRefresh bool
)

var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List local repos (and optionally remote)",
    RunE:  runList,
}

func init() {
    listCmd.Flags().BoolVar(&listJSON, "json", false, "machine-readable output")
    listCmd.Flags().BoolVarP(&listRemote, "remote", "r", false, "include remote listings")
    listCmd.Flags().BoolVar(&listRefresh, "refresh", false, "force refresh of remote cache")
    rootCmd.AddCommand(listCmd)
}

func reposRoot() string {
    if v := os.Getenv("BRIDGE_REPOS_ROOT"); v != "" {
        return v
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, "projects", "repos")
}

func runList(cmd *cobra.Command, args []string) error {
    root := reposRoot()
    repos, err := core.DiscoverRepos(root)
    if err != nil {
        return fmt.Errorf("discover: %w", err)
    }
    sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })

    if listJSON {
        return emitJSON(cmd.OutOrStdout(), repos)
    }
    for _, r := range repos {
        vis := r.Visibility
        if vis == "" {
            vis = "-"
        }
        fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %-8s %s\n", r.Forge, r.Owner, vis, r.Name)
    }
    return nil
}
```

- [ ] **Step 5: Run; expect pass**

`go test ./cmd/bridge -v`. PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/output.go cmd/bridge/list.go cmd/bridge/list_test.go
git commit -m "feat(go): bridge list (local repos, --json)"
```

---

## Task 9: `internal/forge` — Client interface + TTL cache ✅

**Files:**
- Create: `internal/forge/client.go`
- Create: `internal/forge/cache.go`
- Create: `internal/forge/cache_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/forge/cache_test.go`:
```go
package forge

import (
    "encoding/json"
    "path/filepath"
    "testing"
    "time"
)

func TestCacheRoundtrip(t *testing.T) {
    p := filepath.Join(t.TempDir(), "issues.json")
    in := IssueCache{
        UpdatedAt: time.Now().UTC().Truncate(time.Second),
        Issues: []Issue{{Forge: "github", Repo: "f/x", Number: 1, Title: "t"}},
    }
    if err := WriteIssueCache(p, in); err != nil {
        t.Fatal(err)
    }
    out, err := ReadIssueCache(p)
    if err != nil {
        t.Fatal(err)
    }
    if !out.UpdatedAt.Equal(in.UpdatedAt) {
        t.Errorf("ts mismatch: %v vs %v", out.UpdatedAt, in.UpdatedAt)
    }
    if len(out.Issues) != 1 || out.Issues[0].Title != "t" {
        t.Errorf("payload mismatch: %+v", out)
    }
}

func TestCacheStale(t *testing.T) {
    fresh := IssueCache{UpdatedAt: time.Now().Add(-5 * time.Minute)}
    stale := IssueCache{UpdatedAt: time.Now().Add(-30 * time.Minute)}
    if fresh.IsStale(10 * time.Minute) {
        t.Error("fresh should not be stale")
    }
    if !stale.IsStale(10 * time.Minute) {
        t.Error("stale should be stale")
    }
}

func TestReadCacheMissingIsEmpty(t *testing.T) {
    c, err := ReadIssueCache(filepath.Join(t.TempDir(), "missing"))
    if err != nil {
        t.Fatal(err)
    }
    if len(c.Issues) != 0 {
        t.Errorf("want empty, got %+v", c)
    }
}

func TestCacheJSONShape(t *testing.T) {
    in := IssueCache{
        UpdatedAt: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
        Issues:    []Issue{{Forge: "github", Repo: "a/b", Number: 1, Title: "t"}},
    }
    b, _ := json.Marshal(in)
    s := string(b)
    if !contains(s, `"updated_at"`) || !contains(s, `"issues"`) {
        t.Errorf("shape: %s", s)
    }
}

func contains(s, sub string) bool {
    for i := 0; i+len(sub) <= len(s); i++ {
        if s[i:i+len(sub)] == sub {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/forge -v` — FAIL.

- [ ] **Step 3: Implement the Client + Issue types**

Create `internal/forge/client.go`:
```go
package forge

import (
    "context"
    "time"
)

// RepoRef is a remote repo listing (one of many returned by a forge).
type RepoRef struct {
    Forge         string    `json:"forge"`
    Owner         string    `json:"owner"`
    Name          string    `json:"name"`
    DefaultBranch string    `json:"default_branch"`
    Description   string    `json:"description,omitempty"`
    Topics        []string  `json:"topics,omitempty"`
    Visibility    string    `json:"visibility,omitempty"`
    HTMLURL       string    `json:"html_url"`
    SSHURL        string    `json:"ssh_url,omitempty"`
    UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// Issue is an open issue from a forge.
type Issue struct {
    Forge   string    `json:"forge"`
    Repo    string    `json:"repo"`           // "owner/name"
    Number  int       `json:"number"`
    Title   string    `json:"title"`
    URL     string    `json:"url"`
    Labels  []string  `json:"labels,omitempty"`
    Updated time.Time `json:"updated,omitempty"`
}

// Client is implemented by per-forge clients.
type Client interface {
    Name() string                                                       // "github" | "gitlab" | "forgejo"
    ListRepos(ctx context.Context, owner string) ([]RepoRef, error)
    ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error)
}
```

- [ ] **Step 4: Implement the cache**

Create `internal/forge/cache.go`:
```go
package forge

import (
    "encoding/json"
    "time"

    "github.com/freaxnx01/bridge/internal/store"
)

// IssueCache is the on-disk format for cached issues.
type IssueCache struct {
    UpdatedAt time.Time `json:"updated_at"`
    Issues    []Issue   `json:"issues"`
}

// IsStale returns true if UpdatedAt is older than ttl.
func (c IssueCache) IsStale(ttl time.Duration) bool {
    return time.Since(c.UpdatedAt) > ttl
}

// RepoCache is the on-disk format for cached remote repo listings.
type RepoCache struct {
    UpdatedAt time.Time `json:"updated_at"`
    Repos     []RepoRef `json:"repos"`
}

func (c RepoCache) IsStale(ttl time.Duration) bool {
    return time.Since(c.UpdatedAt) > ttl
}

func ReadIssueCache(path string) (IssueCache, error) {
    b, err := store.ReadFile(path)
    if err != nil || len(b) == 0 {
        return IssueCache{}, err
    }
    var c IssueCache
    if err := json.Unmarshal(b, &c); err != nil {
        return IssueCache{}, err
    }
    return c, nil
}

func WriteIssueCache(path string, c IssueCache) error {
    b, err := json.MarshalIndent(c, "", "  ")
    if err != nil {
        return err
    }
    return store.AtomicWrite(path, b)
}

func ReadRepoCache(path string) (RepoCache, error) {
    b, err := store.ReadFile(path)
    if err != nil || len(b) == 0 {
        return RepoCache{}, err
    }
    var c RepoCache
    if err := json.Unmarshal(b, &c); err != nil {
        return RepoCache{}, err
    }
    return c, nil
}

func WriteRepoCache(path string, c RepoCache) error {
    b, err := json.MarshalIndent(c, "", "  ")
    if err != nil {
        return err
    }
    return store.AtomicWrite(path, b)
}
```

- [ ] **Step 5: Run; expect pass**

`go test ./internal/forge -v`. PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/forge/client.go internal/forge/cache.go internal/forge/cache_test.go
git commit -m "feat(go): forge Client interface + TTL cache"
```

---

## Task 10: `internal/forge/github.go` — GitHub client ✅

**Files:**
- Create: `internal/forge/github.go`
- Create: `internal/forge/github_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/forge/github_test.go`:
```go
package forge

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestGithubListRepos(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/users/freaxnx01/repos" {
            t.Errorf("path: %s", r.URL.Path)
        }
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`[
          {"name":"bridge","default_branch":"main","description":"d","topics":["x"],"visibility":"public","html_url":"https://github.com/freaxnx01/bridge","ssh_url":"git@github.com:freaxnx01/bridge.git","updated_at":"2026-05-01T00:00:00Z"},
          {"name":"other","default_branch":"main","visibility":"private","html_url":"https://github.com/freaxnx01/other","updated_at":"2026-05-02T00:00:00Z"}
        ]`))
    }))
    defer srv.Close()

    c := NewGithubClient("token", srv.URL)
    repos, err := c.ListRepos(context.Background(), "freaxnx01")
    if err != nil {
        t.Fatal(err)
    }
    if len(repos) != 2 {
        t.Fatalf("got %d", len(repos))
    }
    if repos[0].Forge != "github" || repos[0].Owner != "freaxnx01" || repos[0].Name != "bridge" {
        t.Errorf("repo[0]: %+v", repos[0])
    }
}

func TestGithubListIssues(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/repos/freaxnx01/bridge/issues" {
            t.Errorf("path: %s", r.URL.Path)
        }
        if r.URL.Query().Get("state") != "open" {
            t.Errorf("state: %s", r.URL.Query().Get("state"))
        }
        w.Write([]byte(`[
          {"number":30,"title":"feat(dashboard)","html_url":"u30","labels":[{"name":"area:tui"}],"updated_at":"2026-05-01T00:00:00Z","pull_request":null},
          {"number":31,"title":"is a PR","html_url":"u31","pull_request":{"url":"x"},"updated_at":"2026-05-02T00:00:00Z"}
        ]`))
    }))
    defer srv.Close()

    c := NewGithubClient("token", srv.URL)
    issues, err := c.ListOpenIssues(context.Background(), "freaxnx01", "bridge")
    if err != nil {
        t.Fatal(err)
    }
    // PRs filtered out
    if len(issues) != 1 {
        t.Fatalf("got %d", len(issues))
    }
    if issues[0].Number != 30 || issues[0].Repo != "freaxnx01/bridge" || issues[0].Labels[0] != "area:tui" {
        t.Errorf("got %+v", issues[0])
    }
}

func TestGithubAuthHeader(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if got := r.Header.Get("Authorization"); got != "Bearer tok" {
            t.Errorf("auth: %q", got)
        }
        w.Write([]byte(`[]`))
    }))
    defer srv.Close()
    c := NewGithubClient("tok", srv.URL)
    _, _ = c.ListRepos(context.Background(), "x")
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/forge -run TestGithub -v`. FAIL.

- [ ] **Step 3: Implement**

Create `internal/forge/github.go`:
```go
package forge

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type GithubClient struct {
    token   string
    baseURL string
    http    *http.Client
}

func NewGithubClient(token, baseURL string) *GithubClient {
    if baseURL == "" {
        baseURL = "https://api.github.com"
    }
    return &GithubClient{
        token:   token,
        baseURL: baseURL,
        http:    &http.Client{Timeout: 15 * time.Second},
    }
}

func (c *GithubClient) Name() string { return "github" }

func (c *GithubClient) get(ctx context.Context, path string, out any) error {
    req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
    if err != nil {
        return err
    }
    if c.token != "" {
        req.Header.Set("Authorization", "Bearer "+c.token)
    }
    req.Header.Set("Accept", "application/vnd.github+json")
    resp, err := c.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("github %s: %s: %s", path, resp.Status, string(body))
    }
    return json.NewDecoder(resp.Body).Decode(out)
}

type ghRepo struct {
    Name          string    `json:"name"`
    DefaultBranch string    `json:"default_branch"`
    Description   string    `json:"description"`
    Topics        []string  `json:"topics"`
    Visibility    string    `json:"visibility"`
    HTMLURL       string    `json:"html_url"`
    SSHURL        string    `json:"ssh_url"`
    UpdatedAt     time.Time `json:"updated_at"`
}

func (c *GithubClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
    var raw []ghRepo
    if err := c.get(ctx, "/users/"+owner+"/repos?per_page=100&type=owner", &raw); err != nil {
        return nil, err
    }
    out := make([]RepoRef, 0, len(raw))
    for _, r := range raw {
        out = append(out, RepoRef{
            Forge:         "github",
            Owner:         owner,
            Name:          r.Name,
            DefaultBranch: r.DefaultBranch,
            Description:   r.Description,
            Topics:        r.Topics,
            Visibility:    r.Visibility,
            HTMLURL:       r.HTMLURL,
            SSHURL:        r.SSHURL,
            UpdatedAt:     r.UpdatedAt,
        })
    }
    return out, nil
}

type ghIssue struct {
    Number      int    `json:"number"`
    Title       string `json:"title"`
    HTMLURL     string `json:"html_url"`
    Labels      []struct{ Name string `json:"name"` } `json:"labels"`
    UpdatedAt   time.Time `json:"updated_at"`
    PullRequest *struct{ URL string `json:"url"` } `json:"pull_request"`
}

func (c *GithubClient) ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
    var raw []ghIssue
    if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/issues?state=open&per_page=100", &raw); err != nil {
        return nil, err
    }
    out := make([]Issue, 0, len(raw))
    for _, i := range raw {
        if i.PullRequest != nil {
            continue
        }
        labels := make([]string, 0, len(i.Labels))
        for _, l := range i.Labels {
            labels = append(labels, l.Name)
        }
        out = append(out, Issue{
            Forge:   "github",
            Repo:    owner + "/" + repo,
            Number:  i.Number,
            Title:   i.Title,
            URL:     i.HTMLURL,
            Labels:  labels,
            Updated: i.UpdatedAt,
        })
    }
    return out, nil
}
```

- [ ] **Step 4: Run; expect pass**

`go test ./internal/forge -v`. PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(go): forge.GithubClient (repos + open issues)"
```

---

## Task 11: `internal/forge` — GitLab + Forgejo clients ✅ (note: test uses r.URL.RawPath, not r.URL.Path as plan said — Go decodes %2F into Path)

These mirror the GitHub pattern: minimal HTTP client, REST list endpoints, mapped into the common `RepoRef`/`Issue` types.

**Files:**
- Create: `internal/forge/gitlab.go`
- Create: `internal/forge/gitlab_test.go`
- Create: `internal/forge/forgejo.go`
- Create: `internal/forge/forgejo_test.go`

- [ ] **Step 1: Write failing GitLab tests**

Create `internal/forge/gitlab_test.go`:
```go
package forge

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestGitlabListRepos(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/v4/users/freaxnx01/projects" {
            t.Errorf("path %s", r.URL.Path)
        }
        if r.Header.Get("PRIVATE-TOKEN") != "tok" {
            t.Errorf("token %q", r.Header.Get("PRIVATE-TOKEN"))
        }
        w.Write([]byte(`[{"name":"glrepo","default_branch":"main","description":"d","visibility":"public","web_url":"u","ssh_url_to_repo":"s","last_activity_at":"2026-05-01T00:00:00Z"}]`))
    }))
    defer srv.Close()
    c := NewGitlabClient("tok", srv.URL)
    repos, err := c.ListRepos(context.Background(), "freaxnx01")
    if err != nil {
        t.Fatal(err)
    }
    if len(repos) != 1 || repos[0].Forge != "gitlab" || repos[0].Name != "glrepo" {
        t.Errorf("%+v", repos)
    }
}

func TestGitlabListIssues(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/v4/projects/freaxnx01%2Fglrepo/issues" {
            t.Errorf("path %s", r.URL.Path)
        }
        w.Write([]byte(`[{"iid":12,"title":"bug","web_url":"u","labels":["a"],"updated_at":"2026-05-02T00:00:00Z"}]`))
    }))
    defer srv.Close()
    c := NewGitlabClient("tok", srv.URL)
    issues, err := c.ListOpenIssues(context.Background(), "freaxnx01", "glrepo")
    if err != nil {
        t.Fatal(err)
    }
    if len(issues) != 1 || issues[0].Number != 12 || issues[0].Labels[0] != "a" {
        t.Errorf("%+v", issues)
    }
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/forge -run TestGitlab -v` → FAIL.

- [ ] **Step 3: Implement GitLab**

Create `internal/forge/gitlab.go`:
```go
package forge

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "time"
)

type GitlabClient struct {
    token   string
    baseURL string
    http    *http.Client
}

func NewGitlabClient(token, baseURL string) *GitlabClient {
    if baseURL == "" {
        baseURL = "https://gitlab.com"
    }
    return &GitlabClient{token: token, baseURL: baseURL, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *GitlabClient) Name() string { return "gitlab" }

func (c *GitlabClient) get(ctx context.Context, path string, out any) error {
    req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
    if err != nil {
        return err
    }
    if c.token != "" {
        req.Header.Set("PRIVATE-TOKEN", c.token)
    }
    resp, err := c.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        b, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("gitlab %s: %s: %s", path, resp.Status, string(b))
    }
    return json.NewDecoder(resp.Body).Decode(out)
}

type glRepo struct {
    Name           string    `json:"name"`
    DefaultBranch  string    `json:"default_branch"`
    Description    string    `json:"description"`
    Visibility     string    `json:"visibility"`
    WebURL         string    `json:"web_url"`
    SSHURLToRepo   string    `json:"ssh_url_to_repo"`
    LastActivityAt time.Time `json:"last_activity_at"`
}

func (c *GitlabClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
    var raw []glRepo
    if err := c.get(ctx, "/api/v4/users/"+owner+"/projects?per_page=100", &raw); err != nil {
        return nil, err
    }
    out := make([]RepoRef, 0, len(raw))
    for _, r := range raw {
        out = append(out, RepoRef{
            Forge: "gitlab", Owner: owner, Name: r.Name,
            DefaultBranch: r.DefaultBranch, Description: r.Description,
            Visibility: r.Visibility, HTMLURL: r.WebURL, SSHURL: r.SSHURLToRepo,
            UpdatedAt: r.LastActivityAt,
        })
    }
    return out, nil
}

type glIssue struct {
    IID       int       `json:"iid"`
    Title     string    `json:"title"`
    WebURL    string    `json:"web_url"`
    Labels    []string  `json:"labels"`
    UpdatedAt time.Time `json:"updated_at"`
}

func (c *GitlabClient) ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
    proj := url.PathEscape(owner + "/" + repo)
    var raw []glIssue
    if err := c.get(ctx, "/api/v4/projects/"+proj+"/issues?state=opened&per_page=100", &raw); err != nil {
        return nil, err
    }
    out := make([]Issue, 0, len(raw))
    for _, i := range raw {
        out = append(out, Issue{
            Forge:  "gitlab",
            Repo:   owner + "/" + repo,
            Number: i.IID,
            Title:  i.Title,
            URL:    i.WebURL,
            Labels: i.Labels,
            Updated: i.UpdatedAt,
        })
    }
    return out, nil
}
```

- [ ] **Step 4: Write failing Forgejo tests**

Create `internal/forge/forgejo_test.go`:
```go
package forge

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestForgejoListRepos(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/v1/users/freax/repos" {
            t.Errorf("path %s", r.URL.Path)
        }
        if r.Header.Get("Authorization") != "token tok" {
            t.Errorf("auth %q", r.Header.Get("Authorization"))
        }
        w.Write([]byte(`[{"name":"fj","default_branch":"main","description":"d","private":false,"html_url":"u","ssh_url":"s","updated_at":"2026-05-01T00:00:00Z"}]`))
    }))
    defer srv.Close()
    c := NewForgejoClient("tok", srv.URL)
    repos, err := c.ListRepos(context.Background(), "freax")
    if err != nil {
        t.Fatal(err)
    }
    if len(repos) != 1 || repos[0].Forge != "forgejo" || repos[0].Visibility != "public" {
        t.Errorf("%+v", repos)
    }
}

func TestForgejoListIssues(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/v1/repos/freax/fj/issues" {
            t.Errorf("path %s", r.URL.Path)
        }
        w.Write([]byte(`[{"number":5,"title":"t","html_url":"u","labels":[{"name":"x"}],"updated_at":"2026-05-02T00:00:00Z","pull_request":null}]`))
    }))
    defer srv.Close()
    c := NewForgejoClient("tok", srv.URL)
    issues, err := c.ListOpenIssues(context.Background(), "freax", "fj")
    if err != nil {
        t.Fatal(err)
    }
    if len(issues) != 1 || issues[0].Number != 5 || issues[0].Labels[0] != "x" {
        t.Errorf("%+v", issues)
    }
}
```

- [ ] **Step 5: Implement Forgejo**

Create `internal/forge/forgejo.go`:
```go
package forge

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type ForgejoClient struct {
    token   string
    baseURL string
    http    *http.Client
}

func NewForgejoClient(token, baseURL string) *ForgejoClient {
    if baseURL == "" {
        baseURL = "https://codeberg.org"
    }
    return &ForgejoClient{token: token, baseURL: baseURL, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *ForgejoClient) Name() string { return "forgejo" }

func (c *ForgejoClient) get(ctx context.Context, path string, out any) error {
    req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
    if c.token != "" {
        req.Header.Set("Authorization", "token "+c.token)
    }
    resp, err := c.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        b, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
    }
    return json.NewDecoder(resp.Body).Decode(out)
}

type fjRepo struct {
    Name          string    `json:"name"`
    DefaultBranch string    `json:"default_branch"`
    Description   string    `json:"description"`
    Private       bool      `json:"private"`
    HTMLURL       string    `json:"html_url"`
    SSHURL        string    `json:"ssh_url"`
    UpdatedAt     time.Time `json:"updated_at"`
}

func (c *ForgejoClient) ListRepos(ctx context.Context, owner string) ([]RepoRef, error) {
    var raw []fjRepo
    if err := c.get(ctx, "/api/v1/users/"+owner+"/repos?limit=50", &raw); err != nil {
        return nil, err
    }
    out := make([]RepoRef, 0, len(raw))
    for _, r := range raw {
        vis := "public"
        if r.Private {
            vis = "private"
        }
        out = append(out, RepoRef{
            Forge: "forgejo", Owner: owner, Name: r.Name,
            DefaultBranch: r.DefaultBranch, Description: r.Description,
            Visibility: vis, HTMLURL: r.HTMLURL, SSHURL: r.SSHURL,
            UpdatedAt: r.UpdatedAt,
        })
    }
    return out, nil
}

type fjIssue struct {
    Number      int       `json:"number"`
    Title       string    `json:"title"`
    HTMLURL     string    `json:"html_url"`
    Labels      []struct{ Name string `json:"name"` } `json:"labels"`
    UpdatedAt   time.Time `json:"updated_at"`
    PullRequest any       `json:"pull_request"`
}

func (c *ForgejoClient) ListOpenIssues(ctx context.Context, owner, repo string) ([]Issue, error) {
    var raw []fjIssue
    if err := c.get(ctx, "/api/v1/repos/"+owner+"/"+repo+"/issues?state=open&type=issues&limit=50", &raw); err != nil {
        return nil, err
    }
    out := make([]Issue, 0, len(raw))
    for _, i := range raw {
        if i.PullRequest != nil {
            continue
        }
        labels := make([]string, 0, len(i.Labels))
        for _, l := range i.Labels {
            labels = append(labels, l.Name)
        }
        out = append(out, Issue{
            Forge: "forgejo", Repo: owner + "/" + repo,
            Number: i.Number, Title: i.Title, URL: i.HTMLURL,
            Labels: labels, Updated: i.UpdatedAt,
        })
    }
    return out, nil
}
```

- [ ] **Step 6: Run all forge tests; expect pass**

`go test ./internal/forge -v`. All PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/forge/gitlab.go internal/forge/gitlab_test.go internal/forge/forgejo.go internal/forge/forgejo_test.go
git commit -m "feat(go): forge.GitlabClient + ForgejoClient"
```

---

## Task 12: `bridge list -r` — remote streaming with cache ✅

**Files:**
- Modify: `cmd/bridge/list.go`
- Create: `cmd/bridge/list_remote_test.go`

`-r` lists local repos AND remote repos (via cached `remote.list` equivalent). `--refresh` re-fetches. Streaming output: as each forge finishes, print its repos. Cache TTL: 1 hour by default. Tokens come from env vars: `GH_TOKEN`, `GITLAB_TOKEN`, `FORGEJO_TOKEN`.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/list_remote_test.go`:
```go
package main

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "testing"
)

func TestListRemoteJSON(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Single GitHub endpoint mock for one owner
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`[{"name":"bridge","default_branch":"main","html_url":"u"}]`))
    }))
    defer srv.Close()

    root := writeFakeRepos(t)
    cacheDir := t.TempDir()

    cmd := exec.Command("go", "run", ".", "list", "-r", "--refresh", "--json")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cacheDir,
        "BRIDGE_GITHUB_API="+srv.URL,
        "GH_TOKEN=tok",
        "BRIDGE_GITLAB_API=",
        "BRIDGE_FORGEJO_API=",
        // Disable gitlab/forgejo by clearing tokens
        "GITLAB_TOKEN=",
        "FORGEJO_TOKEN=",
    )
    var sout stringBuf
    cmd.Stdout = &sout
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var out struct {
        Local  []map[string]any `json:"local"`
        Remote []map[string]any `json:"remote"`
    }
    if err := json.Unmarshal([]byte(sout.String()), &out); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    if len(out.Local) == 0 {
        t.Errorf("expected local repos")
    }
    if len(out.Remote) == 0 {
        t.Errorf("expected remote repos")
    }
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./cmd/bridge -run TestListRemote -v`. FAIL (`-r` not implemented).

- [ ] **Step 3: Implement `-r` path**

Replace the `runList` body in `cmd/bridge/list.go`:
```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
    "github.com/freaxnx01/bridge/internal/forge"
    "github.com/freaxnx01/bridge/internal/store"
)

var (
    listJSON    bool
    listRemote  bool
    listRefresh bool
)

var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List local repos (and optionally remote)",
    RunE:  runList,
}

func init() {
    listCmd.Flags().BoolVar(&listJSON, "json", false, "machine-readable output")
    listCmd.Flags().BoolVarP(&listRemote, "remote", "r", false, "include remote listings")
    listCmd.Flags().BoolVar(&listRefresh, "refresh", false, "force refresh of remote cache")
    rootCmd.AddCommand(listCmd)
}

func reposRoot() string {
    if v := os.Getenv("BRIDGE_REPOS_ROOT"); v != "" {
        return v
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, "projects", "repos")
}

func cacheRoot() string {
    if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
        return filepath.Join(v, "bridge")
    }
    d, _ := store.Dir()
    return d
}

func runList(cmd *cobra.Command, args []string) error {
    root := reposRoot()
    local, err := core.DiscoverRepos(root)
    if err != nil {
        return fmt.Errorf("discover: %w", err)
    }
    sort.Slice(local, func(i, j int) bool { return local[i].Path < local[j].Path })

    if !listRemote {
        if listJSON {
            return emitJSON(cmd.OutOrStdout(), local)
        }
        for _, r := range local {
            vis := r.Visibility
            if vis == "" {
                vis = "-"
            }
            fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %-8s %s\n", r.Forge, r.Owner, vis, r.Name)
        }
        return nil
    }

    remote, err := loadOrFetchRemote(cmd.Context(), local, listRefresh)
    if err != nil {
        fmt.Fprintf(os.Stderr, "warning: remote fetch failed, using cache: %v\n", err)
    }
    if listJSON {
        return emitJSON(cmd.OutOrStdout(), struct {
            Local  []core.Repo     `json:"local"`
            Remote []forge.RepoRef `json:"remote"`
        }{local, remote})
    }
    fmt.Fprintln(cmd.OutOrStdout(), "# local")
    for _, r := range local {
        fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %s\n", r.Forge, r.Owner, r.Name)
    }
    fmt.Fprintln(cmd.OutOrStdout(), "# remote")
    for _, r := range remote {
        fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %s\n", r.Forge, r.Owner, r.Name)
    }
    return nil
}

const remoteTTL = time.Hour

func loadOrFetchRemote(ctx context.Context, local []core.Repo, refresh bool) ([]forge.RepoRef, error) {
    cachePath := filepath.Join(cacheRoot(), "remote.list")
    if !refresh {
        c, err := forge.ReadRepoCache(cachePath)
        if err == nil && !c.IsStale(remoteTTL) && len(c.Repos) > 0 {
            return c.Repos, nil
        }
    }
    owners := uniqueOwners(local)
    var all []forge.RepoRef
    var firstErr error
    if api := os.Getenv("BRIDGE_GITHUB_API"); api != "" || os.Getenv("GH_TOKEN") != "" {
        c := forge.NewGithubClient(os.Getenv("GH_TOKEN"), api)
        for _, o := range owners["github"] {
            r, err := c.ListRepos(ctx, o)
            if err != nil {
                if firstErr == nil {
                    firstErr = err
                }
                continue
            }
            all = append(all, r...)
        }
    }
    if api := os.Getenv("BRIDGE_GITLAB_API"); api != "" || os.Getenv("GITLAB_TOKEN") != "" {
        c := forge.NewGitlabClient(os.Getenv("GITLAB_TOKEN"), api)
        for _, o := range owners["gitlab"] {
            r, err := c.ListRepos(ctx, o)
            if err != nil {
                if firstErr == nil {
                    firstErr = err
                }
                continue
            }
            all = append(all, r...)
        }
    }
    if api := os.Getenv("BRIDGE_FORGEJO_API"); api != "" || os.Getenv("FORGEJO_TOKEN") != "" {
        c := forge.NewForgejoClient(os.Getenv("FORGEJO_TOKEN"), api)
        for _, o := range owners["forgejo"] {
            r, err := c.ListRepos(ctx, o)
            if err != nil {
                if firstErr == nil {
                    firstErr = err
                }
                continue
            }
            all = append(all, r...)
        }
    }
    _ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
    return all, firstErr
}

func uniqueOwners(local []core.Repo) map[string][]string {
    seen := map[string]map[string]bool{}
    for _, r := range local {
        if seen[r.Forge] == nil {
            seen[r.Forge] = map[string]bool{}
        }
        seen[r.Forge][r.Owner] = true
    }
    out := map[string][]string{}
    for forge, owners := range seen {
        for o := range owners {
            out[forge] = append(out[forge], o)
        }
    }
    return out
}
```

- [ ] **Step 4: Run; expect pass**

`go test ./cmd/bridge -v`. PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/list.go cmd/bridge/list_remote_test.go
git commit -m "feat(go): bridge list -r --refresh with remote cache"
```

---

## Task 13: `internal/core` — Slot type + read ✅

**Files:**
- Create: `internal/core/slot.go`
- Create: `internal/core/slot_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/core/slot_test.go`:
```go
package core

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadSlots(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "slots.json")
    _ = os.WriteFile(p, []byte(`{"slots":[
      {"id":"bridge-main","repo":"bridge","worktree":"","agent":"claude","created":"2026-05-01T00:00:00Z"},
      {"id":"ingest-bug","repo":"ingest","worktree":"bug-142","agent":"copilot","created":"2026-05-02T00:00:00Z"}
    ]}`), 0o644)
    slots, err := LoadSlots(p)
    if err != nil {
        t.Fatal(err)
    }
    if len(slots) != 2 {
        t.Fatalf("got %d", len(slots))
    }
    if slots[0].ID != "bridge-main" || slots[1].Worktree != "bug-142" {
        t.Errorf("%+v", slots)
    }
}

func TestLoadSlotsMissing(t *testing.T) {
    slots, err := LoadSlots(filepath.Join(t.TempDir(), "missing"))
    if err != nil {
        t.Fatal(err)
    }
    if len(slots) != 0 {
        t.Errorf("want empty, got %v", slots)
    }
}
```

- [ ] **Step 2: Run; expect failure**

`go test ./internal/core -run TestLoadSlots -v`. FAIL.

- [ ] **Step 3: Implement**

Create `internal/core/slot.go`:
```go
package core

import (
    "encoding/json"
    "time"

    "github.com/freaxnx01/bridge/internal/store"
)

type Slot struct {
    ID       string    `json:"id"`
    Repo     string    `json:"repo"`
    Worktree string    `json:"worktree,omitempty"`
    Agent    string    `json:"agent"`
    Created  time.Time `json:"created"`
}

type slotFile struct {
    Slots []Slot `json:"slots"`
}

func LoadSlots(path string) ([]Slot, error) {
    b, err := store.ReadFile(path)
    if err != nil || len(b) == 0 {
        return nil, err
    }
    var f slotFile
    if err := json.Unmarshal(b, &f); err != nil {
        return nil, err
    }
    return f.Slots, nil
}
```

- [ ] **Step 4: Run; PASS**

`go test ./internal/core -v`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/slot.go internal/core/slot_test.go
git commit -m "feat(go): core.Slot type + LoadSlots"
```

---

## Task 14: `bridge slots` command ✅

**Files:**
- Create: `cmd/bridge/slots.go`
- Create: `cmd/bridge/slots_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/slots_test.go`:
```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func writeSlots(t *testing.T) string {
    t.Helper()
    cache := t.TempDir()
    cacheDir := filepath.Join(cache, "bridge")
    _ = os.MkdirAll(cacheDir, 0o755)
    _ = os.WriteFile(filepath.Join(cacheDir, "slots.json"), []byte(`{"slots":[
      {"id":"a","repo":"x","agent":"claude","created":"2026-05-01T00:00:00Z"}
    ]}`), 0o644)
    return cache
}

func TestSlotsJSON(t *testing.T) {
    cache := writeSlots(t)
    cmd := exec.Command("go", "run", ".", "slots", "--json")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var slots []map[string]any
    if err := json.Unmarshal([]byte(sout.String()), &slots); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    if len(slots) != 1 || slots[0]["id"] != "a" {
        t.Errorf("%+v", slots)
    }
}

func TestSlotsHuman(t *testing.T) {
    cache := writeSlots(t)
    cmd := exec.Command("go", "run", ".", "slots")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    if !contains(sout.String(), "a") || !contains(sout.String(), "claude") {
        t.Errorf("got %s", sout.String())
    }
}
```

- [ ] **Step 2: Run; FAIL**

`go test ./cmd/bridge -run TestSlots -v`. FAIL.

- [ ] **Step 3: Implement**

Create `cmd/bridge/slots.go`:
```go
package main

import (
    "fmt"
    "path/filepath"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
)

var slotsJSON bool

var slotsCmd = &cobra.Command{
    Use:   "slots",
    Short: "Show slot registry",
    RunE:  runSlots,
}

func init() {
    slotsCmd.Flags().BoolVar(&slotsJSON, "json", false, "machine-readable output")
    rootCmd.AddCommand(slotsCmd)
}

func runSlots(cmd *cobra.Command, args []string) error {
    slots, err := core.LoadSlots(filepath.Join(cacheRoot(), "slots.json"))
    if err != nil {
        return err
    }
    if slotsJSON {
        return emitJSON(cmd.OutOrStdout(), slots)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-20s %-10s %s\n", "id", "repo", "agent", "created")
    for _, s := range slots {
        wt := s.Worktree
        if wt == "" {
            wt = "-"
        }
        fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-20s %-10s %s (wt=%s)\n", s.ID, s.Repo, s.Agent, s.Created.Format("2006-01-02 15:04"), wt)
    }
    return nil
}
```

- [ ] **Step 4: Run; PASS**

`go test ./cmd/bridge -v`.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/slots.go cmd/bridge/slots_test.go
git commit -m "feat(go): bridge slots"
```

---

## Task 15: `internal/core` — Session inspection (tmux read) ✅

**Files:**
- Create: `internal/core/session.go`
- Create: `internal/core/session_test.go`

Live sessions come from `tmux list-sessions -F "<format>"`. We shell out and parse, but tests inject the executor.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/session_test.go`:
```go
package core

import (
    "testing"
)

func TestParseTmuxList(t *testing.T) {
    // format used: "#{session_name}|#{session_attached}|#{session_created}"
    raw := `bridge-main|1|1716000000
ingest-bug|0|1716000100
`
    sessions, err := ParseTmuxList(raw, 1716000200)
    if err != nil {
        t.Fatal(err)
    }
    if len(sessions) != 2 {
        t.Fatalf("got %d", len(sessions))
    }
    if sessions[0].SlotID != "bridge-main" || sessions[0].State != "attached" {
        t.Errorf("[0]: %+v", sessions[0])
    }
    if sessions[1].State != "detached" {
        t.Errorf("[1]: %+v", sessions[1])
    }
    if sessions[0].Age <= 0 || sessions[1].Age <= 0 {
        t.Errorf("age: %v %v", sessions[0].Age, sessions[1].Age)
    }
}

func TestParseTmuxListEmpty(t *testing.T) {
    sessions, err := ParseTmuxList("", 0)
    if err != nil {
        t.Fatal(err)
    }
    if sessions != nil {
        t.Errorf("expected nil, got %v", sessions)
    }
}

func TestParseTmuxListBadLine(t *testing.T) {
    _, err := ParseTmuxList("only-one-field\n", 0)
    if err == nil {
        t.Error("expected error for malformed line")
    }
}
```

- [ ] **Step 2: Run; FAIL**

`go test ./internal/core -run TestParseTmux -v`. FAIL.

- [ ] **Step 3: Implement**

Create `internal/core/session.go`:
```go
package core

import (
    "bufio"
    "errors"
    "fmt"
    "os/exec"
    "strconv"
    "strings"
    "time"
)

type Session struct {
    SlotID   string        `json:"slot_id"`
    State    string        `json:"state"`
    Age      time.Duration `json:"age"`
    PID      int           `json:"pid,omitempty"`
    TmuxName string        `json:"tmux_name"`
}

// ParseTmuxList parses tmux ls output in format "name|attached|created_unix".
// nowUnix is current unix time (for testability).
func ParseTmuxList(raw string, nowUnix int64) ([]Session, error) {
    if strings.TrimSpace(raw) == "" {
        return nil, nil
    }
    var out []Session
    sc := bufio.NewScanner(strings.NewReader(raw))
    for sc.Scan() {
        line := strings.TrimSpace(sc.Text())
        if line == "" {
            continue
        }
        parts := strings.Split(line, "|")
        if len(parts) != 3 {
            return nil, fmt.Errorf("malformed tmux line: %q", line)
        }
        attached, _ := strconv.Atoi(parts[1])
        created, err := strconv.ParseInt(parts[2], 10, 64)
        if err != nil {
            return nil, fmt.Errorf("created: %w", err)
        }
        state := "detached"
        if attached > 0 {
            state = "attached"
        }
        out = append(out, Session{
            SlotID:   parts[0],
            TmuxName: parts[0],
            State:    state,
            Age:      time.Duration(nowUnix-created) * time.Second,
        })
    }
    return out, nil
}

// LiveSessions calls tmux and returns active sessions. Returns empty if tmux missing.
func LiveSessions() ([]Session, error) {
    cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}|#{session_attached}|#{session_created}")
    out, err := cmd.Output()
    if err != nil {
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            // tmux exits non-zero when no server is running — treat as empty.
            return nil, nil
        }
        if errors.Is(err, exec.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }
    return ParseTmuxList(string(out), time.Now().Unix())
}
```

- [ ] **Step 4: Run; PASS**

`go test ./internal/core -v`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/session.go internal/core/session_test.go
git commit -m "feat(go): core.Session + tmux parse"
```

---

## Task 16: `bridge sessions` command ✅

**Files:**
- Create: `cmd/bridge/sessions.go`
- Create: `cmd/bridge/sessions_test.go`

For testability we read tmux output from `BRIDGE_TMUX_FIXTURE` if set (a file path with raw `tmux ls` output) — never expose this flag to users.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/sessions_test.go`:
```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestSessionsJSON(t *testing.T) {
    dir := t.TempDir()
    fixture := filepath.Join(dir, "tmux.txt")
    _ = os.WriteFile(fixture, []byte("a|1|1716000000\nb|0|1716000100\n"), 0o644)

    cmd := exec.Command("go", "run", ".", "sessions", "--json")
    cmd.Env = append(os.Environ(),
        "BRIDGE_TMUX_FIXTURE="+fixture,
        "BRIDGE_NOW=1716000200",
    )
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var sess []map[string]any
    if err := json.Unmarshal([]byte(sout.String()), &sess); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    if len(sess) != 2 || sess[0]["state"] != "attached" || sess[1]["state"] != "detached" {
        t.Errorf("%+v", sess)
    }
}
```

- [ ] **Step 2: Run; FAIL**

`go test ./cmd/bridge -run TestSessions -v`. FAIL.

- [ ] **Step 3: Implement**

Create `cmd/bridge/sessions.go`:
```go
package main

import (
    "fmt"
    "os"
    "strconv"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
)

var sessionsJSON bool

var sessionsCmd = &cobra.Command{
    Use:   "sessions",
    Short: "Show live agent sessions",
    RunE:  runSessions,
}

func init() {
    sessionsCmd.Flags().BoolVar(&sessionsJSON, "json", false, "machine-readable output")
    rootCmd.AddCommand(sessionsCmd)
}

func runSessions(cmd *cobra.Command, args []string) error {
    sessions, err := loadSessions()
    if err != nil {
        return err
    }
    if sessionsJSON {
        return emitJSON(cmd.OutOrStdout(), sessions)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %s\n", "slot", "state", "age")
    for _, s := range sessions {
        fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %s\n", s.SlotID, s.State, humanDuration(s.Age))
    }
    return nil
}

func loadSessions() ([]core.Session, error) {
    if f := os.Getenv("BRIDGE_TMUX_FIXTURE"); f != "" {
        b, err := os.ReadFile(f)
        if err != nil {
            return nil, err
        }
        now := time.Now().Unix()
        if v := os.Getenv("BRIDGE_NOW"); v != "" {
            if n, err := strconv.ParseInt(v, 10, 64); err == nil {
                now = n
            }
        }
        return core.ParseTmuxList(string(b), now)
    }
    return core.LiveSessions()
}

func humanDuration(d time.Duration) string {
    if d < time.Minute {
        return fmt.Sprintf("%ds", int(d.Seconds()))
    }
    if d < time.Hour {
        return fmt.Sprintf("%dm", int(d.Minutes()))
    }
    if d < 24*time.Hour {
        return fmt.Sprintf("%dh", int(d.Hours()))
    }
    return fmt.Sprintf("%dd", int(d.Hours()/24))
}
```

- [ ] **Step 4: Run; PASS**

`go test ./cmd/bridge -v`.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/sessions.go cmd/bridge/sessions_test.go
git commit -m "feat(go): bridge sessions"
```

---

## Task 17: `internal/core` — Presence read ✅

**Files:**
- Create: `internal/core/presence.go`
- Create: `internal/core/presence_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/core/presence_test.go`:
```go
package core

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadPresence(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "presence.json")
    _ = os.WriteFile(p, []byte(`{"mode":"away","overrides":{"a":"on"},"updated_at":"2026-05-01T00:00:00Z"}`), 0o644)
    pr, err := LoadPresence(p)
    if err != nil {
        t.Fatal(err)
    }
    if pr.Mode != "away" || pr.Overrides["a"] != "on" {
        t.Errorf("%+v", pr)
    }
}

func TestLoadPresenceMissingDefaults(t *testing.T) {
    pr, err := LoadPresence(filepath.Join(t.TempDir(), "missing"))
    if err != nil {
        t.Fatal(err)
    }
    if pr.Mode != "auto" {
        t.Errorf("default mode: %s", pr.Mode)
    }
}
```

- [ ] **Step 2: FAIL → implement**

Create `internal/core/presence.go`:
```go
package core

import (
    "encoding/json"
    "time"

    "github.com/freaxnx01/bridge/internal/store"
)

type Presence struct {
    Mode      string            `json:"mode"`               // "auto" | "away" | "back"
    Overrides map[string]string `json:"overrides,omitempty"`
    UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

func LoadPresence(path string) (Presence, error) {
    b, err := store.ReadFile(path)
    if err != nil {
        return Presence{Mode: "auto"}, err
    }
    if len(b) == 0 {
        return Presence{Mode: "auto"}, nil
    }
    var p Presence
    if err := json.Unmarshal(b, &p); err != nil {
        return Presence{Mode: "auto"}, err
    }
    if p.Mode == "" {
        p.Mode = "auto"
    }
    return p, nil
}
```

- [ ] **Step 3: PASS → commit**

```bash
git add internal/core/presence.go internal/core/presence_test.go
git commit -m "feat(go): core.Presence + LoadPresence"
```

---

## Task 18: `bridge presence` (read only) ✅

**Files:**
- Create: `cmd/bridge/presence.go`
- Create: `cmd/bridge/presence_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/presence_test.go`:
```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestPresenceReadJSON(t *testing.T) {
    cache := t.TempDir()
    cacheDir := filepath.Join(cache, "bridge")
    _ = os.MkdirAll(cacheDir, 0o755)
    _ = os.WriteFile(filepath.Join(cacheDir, "presence.json"), []byte(`{"mode":"away"}`), 0o644)

    cmd := exec.Command("go", "run", ".", "presence", "--json")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var p map[string]any
    if err := json.Unmarshal([]byte(sout.String()), &p); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    if p["mode"] != "away" {
        t.Errorf("got %+v", p)
    }
}

func TestPresenceWriteRejected(t *testing.T) {
    // Plan A is read-only — writes belong to Plan B.
    cmd := exec.Command("go", "run", ".", "presence", "away")
    var serr stringBuf
    cmd.Stderr = &serr
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit")
    }
}
```

- [ ] **Step 2: FAIL → implement**

Create `cmd/bridge/presence.go`:
```go
package main

import (
    "fmt"
    "path/filepath"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
)

var presenceJSON bool

var presenceCmd = &cobra.Command{
    Use:   "presence [away|back|auto]",
    Short: "Show presence (read-only in Plan A)",
    Args:  cobra.MaximumNArgs(1),
    RunE:  runPresence,
}

func init() {
    presenceCmd.Flags().BoolVar(&presenceJSON, "json", false, "machine-readable output")
    rootCmd.AddCommand(presenceCmd)
}

func runPresence(cmd *cobra.Command, args []string) error {
    if len(args) > 0 {
        return fmt.Errorf("setting presence is not implemented yet (Plan B); read-only in Plan A")
    }
    p, err := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
    if err != nil {
        return err
    }
    if presenceJSON {
        return emitJSON(cmd.OutOrStdout(), p)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\n", p.Mode)
    if len(p.Overrides) > 0 {
        fmt.Fprintln(cmd.OutOrStdout(), "overrides:")
        for k, v := range p.Overrides {
            fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", k, v)
        }
    }
    return nil
}
```

- [ ] **Step 3: PASS → commit**

```bash
git add cmd/bridge/presence.go cmd/bridge/presence_test.go
git commit -m "feat(go): bridge presence (read-only)"
```

---

## Task 19: `bridge sync` — status summary (read-only) ✅

**Files:**
- Create: `cmd/bridge/sync.go`
- Create: `cmd/bridge/sync_test.go`

`bridge sync` (no args) reads `~/.cache/bridge/sync.json` if present and reports last-run + queue + unpushed count. The actual writing of that file is a Plan B concern (long-running `sync --auto`). For Plan A we only render.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/sync_test.go`:
```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestSyncStatusJSON(t *testing.T) {
    cache := t.TempDir()
    cacheDir := filepath.Join(cache, "bridge")
    _ = os.MkdirAll(cacheDir, 0o755)
    _ = os.WriteFile(filepath.Join(cacheDir, "sync.json"), []byte(`{
      "last_run":"2026-05-01T00:00:00Z","queue":["a","b"],"unpushed":["repo/x"]
    }`), 0o644)

    cmd := exec.Command("go", "run", ".", "sync", "--json")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var s map[string]any
    if err := json.Unmarshal([]byte(sout.String()), &s); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    if len(s["queue"].([]any)) != 2 || len(s["unpushed"].([]any)) != 1 {
        t.Errorf("%+v", s)
    }
}

func TestSyncStatusMissing(t *testing.T) {
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "sync", "--json")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var s map[string]any
    _ = json.Unmarshal([]byte(sout.String()), &s)
    // Missing state file → zero values, no error
    if s == nil {
        t.Errorf("expected object even when missing")
    }
}

func TestSyncNowNotImplemented(t *testing.T) {
    cmd := exec.Command("go", "run", ".", "sync", "now")
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit (Plan B)")
    }
}
```

- [ ] **Step 2: FAIL → implement**

Create `cmd/bridge/sync.go`:
```go
package main

import (
    "encoding/json"
    "fmt"
    "path/filepath"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/store"
)

type SyncState struct {
    LastRun  time.Time `json:"last_run,omitempty"`
    Queue    []string  `json:"queue,omitempty"`
    Unpushed []string  `json:"unpushed,omitempty"`
}

var syncJSON bool

var syncCmd = &cobra.Command{
    Use:   "sync [now|--auto]",
    Short: "Show autosync state (read-only in Plan A)",
    Args:  cobra.MaximumNArgs(1),
    RunE:  runSync,
}

func init() {
    syncCmd.Flags().BoolVar(&syncJSON, "json", false, "machine-readable output")
    rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
    if len(args) > 0 {
        return fmt.Errorf("`bridge sync %s` is not implemented yet (Plan B); read-only status in Plan A", args[0])
    }
    b, err := store.ReadFile(filepath.Join(cacheRoot(), "sync.json"))
    if err != nil {
        return err
    }
    var s SyncState
    if len(b) > 0 {
        if err := json.Unmarshal(b, &s); err != nil {
            return err
        }
    }
    if syncJSON {
        return emitJSON(cmd.OutOrStdout(), s)
    }
    if s.LastRun.IsZero() {
        fmt.Fprintln(cmd.OutOrStdout(), "last run: never")
    } else {
        fmt.Fprintf(cmd.OutOrStdout(), "last run: %s\n", s.LastRun.Format(time.RFC3339))
    }
    fmt.Fprintf(cmd.OutOrStdout(), "queue: %d\n", len(s.Queue))
    fmt.Fprintf(cmd.OutOrStdout(), "unpushed: %d\n", len(s.Unpushed))
    for _, r := range s.Unpushed {
        fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", r)
    }
    return nil
}
```

- [ ] **Step 3: PASS → commit**

```bash
git add cmd/bridge/sync.go cmd/bridge/sync_test.go
git commit -m "feat(go): bridge sync (read-only status)"
```

---

## Task 20: `bridge issues` — fetch + cache + render ✅

**Files:**
- Create: `internal/core/issue.go`
- Create: `cmd/bridge/issues.go`
- Create: `cmd/bridge/issues_test.go`

- [ ] **Step 1: Define the domain alias for `Issue`**

Create `internal/core/issue.go`:
```go
package core

import "github.com/freaxnx01/bridge/internal/forge"

// Issue is re-exported from forge so callers stay within core for domain types.
type Issue = forge.Issue
```

- [ ] **Step 2: Write the failing test**

Create `cmd/bridge/issues_test.go`:
```go
package main

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "testing"
)

func TestIssuesFetchAndCache(t *testing.T) {
    calls := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        w.Write([]byte(`[{"number":1,"title":"t","html_url":"u","updated_at":"2026-05-01T00:00:00Z"}]`))
    }))
    defer srv.Close()

    root := writeFakeRepos(t)
    cache := t.TempDir()

    common := append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
        "BRIDGE_GITHUB_API="+srv.URL,
        "GH_TOKEN=tok",
        "GITLAB_TOKEN=",
        "FORGEJO_TOKEN=",
    )

    // First call: should hit network.
    cmd := exec.Command("go", "run", ".", "issues", "--json", "--refresh")
    cmd.Env = common
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run1: %v", err)
    }
    var issues []map[string]any
    if err := json.Unmarshal([]byte(sout.String()), &issues); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    if len(issues) == 0 {
        t.Errorf("expected issues, got %v", issues)
    }
    if calls == 0 {
        t.Errorf("expected network calls")
    }

    // Second call without --refresh: should use cache.
    callsBefore := calls
    cmd2 := exec.Command("go", "run", ".", "issues", "--json")
    cmd2.Env = common
    var sout2 stringBuf
    cmd2.Stdout = &sout2
    if err := cmd2.Run(); err != nil {
        t.Fatalf("run2: %v", err)
    }
    if calls != callsBefore {
        t.Errorf("expected no additional network calls; before=%d after=%d", callsBefore, calls)
    }
}
```

- [ ] **Step 3: FAIL → implement**

Create `cmd/bridge/issues.go`:
```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
    "github.com/freaxnx01/bridge/internal/forge"
)

var (
    issuesJSON    bool
    issuesRefresh bool
)

var issuesCmd = &cobra.Command{
    Use:   "issues",
    Short: "List open issues across forges (cached)",
    RunE:  runIssues,
}

func init() {
    issuesCmd.Flags().BoolVar(&issuesJSON, "json", false, "machine-readable output")
    issuesCmd.Flags().BoolVar(&issuesRefresh, "refresh", false, "force refresh of issues cache")
    rootCmd.AddCommand(issuesCmd)
}

const issuesTTL = 10 * time.Minute

func runIssues(cmd *cobra.Command, args []string) error {
    cachePath := filepath.Join(cacheRoot(), "issues.json")
    if !issuesRefresh {
        c, err := forge.ReadIssueCache(cachePath)
        if err == nil && !c.IsStale(issuesTTL) && len(c.Issues) > 0 {
            return renderIssues(cmd, c.Issues)
        }
    }
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return err
    }
    var all []forge.Issue
    var firstErr error
    ctx := context.Background()
    for _, r := range repos {
        client := clientFor(r.Forge)
        if client == nil {
            continue
        }
        issues, err := client.ListOpenIssues(ctx, r.Owner, r.Name)
        if err != nil {
            if firstErr == nil {
                firstErr = err
            }
            continue
        }
        all = append(all, issues...)
    }
    _ = forge.WriteIssueCache(cachePath, forge.IssueCache{UpdatedAt: time.Now(), Issues: all})
    if err := renderIssues(cmd, all); err != nil {
        return err
    }
    return firstErr
}

func renderIssues(cmd *cobra.Command, issues []forge.Issue) error {
    if issuesJSON {
        return emitJSON(cmd.OutOrStdout(), issues)
    }
    for _, i := range issues {
        fmt.Fprintf(cmd.OutOrStdout(), "%-8s %-30s #%-5d %s\n", i.Forge, i.Repo, i.Number, i.Title)
    }
    return nil
}

func clientFor(name string) forge.Client {
    switch name {
    case "github":
        if t := os.Getenv("GH_TOKEN"); t != "" || os.Getenv("BRIDGE_GITHUB_API") != "" {
            return forge.NewGithubClient(t, os.Getenv("BRIDGE_GITHUB_API"))
        }
    case "gitlab":
        if t := os.Getenv("GITLAB_TOKEN"); t != "" || os.Getenv("BRIDGE_GITLAB_API") != "" {
            return forge.NewGitlabClient(t, os.Getenv("BRIDGE_GITLAB_API"))
        }
    case "forgejo":
        if t := os.Getenv("FORGEJO_TOKEN"); t != "" || os.Getenv("BRIDGE_FORGEJO_API") != "" {
            return forge.NewForgejoClient(t, os.Getenv("BRIDGE_FORGEJO_API"))
        }
    }
    return nil
}
```

- [ ] **Step 4: Run; PASS**

`go test ./cmd/bridge -v`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/issue.go cmd/bridge/issues.go cmd/bridge/issues_test.go
git commit -m "feat(go): bridge issues (fetch + 10min cache)"
```

---

## Task 21: `bridge status` — slim composed summary ✅

**Files:**
- Create: `cmd/bridge/status.go`
- Create: `cmd/bridge/status_test.go`

The new slim status replaces the overloaded bash `--status`. Output is ≤10 lines: session count, presence mode, last sync, unpushed count, version line.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/status_test.go`:
```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestStatusHuman(t *testing.T) {
    cache := t.TempDir()
    cacheDir := filepath.Join(cache, "bridge")
    _ = os.MkdirAll(cacheDir, 0o755)
    _ = os.WriteFile(filepath.Join(cacheDir, "presence.json"), []byte(`{"mode":"away"}`), 0o644)
    _ = os.WriteFile(filepath.Join(cacheDir, "sync.json"), []byte(`{"unpushed":["x"]}`), 0o644)

    cmd := exec.Command("go", "run", ".", "status")
    cmd.Env = append(os.Environ(),
        "XDG_CACHE_HOME="+cache,
        "BRIDGE_TMUX_FIXTURE=", // no tmux fixture → 0 sessions
    )
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    s := sout.String()
    if !contains(s, "presence:") || !contains(s, "away") || !contains(s, "unpushed:") {
        t.Errorf("missing keys in %s", s)
    }
}

func TestStatusJSON(t *testing.T) {
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "status", "--json")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    var st map[string]any
    if err := json.Unmarshal([]byte(sout.String()), &st); err != nil {
        t.Fatalf("json: %v in %s", err, sout.String())
    }
    for _, k := range []string{"sessions", "presence", "sync", "version"} {
        if _, ok := st[k]; !ok {
            t.Errorf("missing key %s in %+v", k, st)
        }
    }
}
```

- [ ] **Step 2: FAIL → implement**

Create `cmd/bridge/status.go`:
```go
package main

import (
    "encoding/json"
    "fmt"
    "path/filepath"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
    "github.com/freaxnx01/bridge/internal/store"
)

var statusJSON bool

var statusCmd = &cobra.Command{
    Use:   "status",
    Short: "Slim composed summary",
    RunE:  runStatus,
}

func init() {
    statusCmd.Flags().BoolVar(&statusJSON, "json", false, "machine-readable output")
    rootCmd.AddCommand(statusCmd)
}

type statusOut struct {
    Sessions int    `json:"sessions"`
    Presence string `json:"presence"`
    Sync     struct {
        Unpushed int `json:"unpushed"`
    } `json:"sync"`
    Version string `json:"version"`
}

func runStatus(cmd *cobra.Command, args []string) error {
    var st statusOut
    st.Version = versionString()

    sessions, _ := loadSessions()
    st.Sessions = len(sessions)

    pr, _ := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
    st.Presence = pr.Mode

    if b, err := store.ReadFile(filepath.Join(cacheRoot(), "sync.json")); err == nil && len(b) > 0 {
        var sy SyncState
        if err := json.Unmarshal(b, &sy); err == nil {
            st.Sync.Unpushed = len(sy.Unpushed)
        }
    }

    if statusJSON {
        return emitJSON(cmd.OutOrStdout(), st)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "sessions:  %d\n", st.Sessions)
    fmt.Fprintf(cmd.OutOrStdout(), "presence:  %s\n", st.Presence)
    fmt.Fprintf(cmd.OutOrStdout(), "unpushed:  %d\n", st.Sync.Unpushed)
    fmt.Fprintf(cmd.OutOrStdout(), "version:   %s\n", st.Version)
    return nil
}
```

- [ ] **Step 3: PASS → commit**

```bash
git add cmd/bridge/status.go cmd/bridge/status_test.go
git commit -m "feat(go): bridge status (slim composed summary)"
```

---

## Task 22: Bash format read-compat — MRU coexistence ✅

The bash `bridge` writes `~/.cache/bridge/mru` as newline-delimited paths (oldest at top, newest at bottom). Our `LoadMRU` already reads that format. This task adds a smoke test that proves it.

**Files:**
- Create: `internal/core/mru_compat_test.go`

- [ ] **Step 1: Write the compat test**

Create `internal/core/mru_compat_test.go`:
```go
package core

import (
    "os"
    "path/filepath"
    "testing"
)

// Mirrors the format bash bridge writes today.
func TestMRUBashCompat(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "mru")
    if err := os.WriteFile(p, []byte(
        "/home/me/projects/repos/github/me/public/old-thing\n"+
            "/home/me/projects/repos/github/me/private/secret\n"+
            "/home/me/projects/repos/github/me/public/bridge\n",
    ), 0o644); err != nil {
        t.Fatal(err)
    }
    got, err := LoadMRU(p)
    if err != nil {
        t.Fatal(err)
    }
    want := []string{
        "/home/me/projects/repos/github/me/public/bridge",
        "/home/me/projects/repos/github/me/private/secret",
        "/home/me/projects/repos/github/me/public/old-thing",
    }
    if len(got) != len(want) {
        t.Fatalf("got %v want %v", got, want)
    }
    for i := range want {
        if got[i] != want[i] {
            t.Errorf("[%d] %s vs %s", i, got[i], want[i])
        }
    }
}
```

- [ ] **Step 2: Run; expect PASS (no implementation change needed)**

`go test ./internal/core -run TestMRUBashCompat -v`.

- [ ] **Step 3: Commit**

```bash
git add internal/core/mru_compat_test.go
git commit -m "test(go): MRU bash format compatibility smoke test"
```

---

## Task 23: `--json` schema doc + ship as `bridge-go` ✅

**Files:**
- Create: `docs/cli-json-schema.md`
- Modify: `Makefile` (add `install-go` target)

- [ ] **Step 1: Write `docs/cli-json-schema.md`**

Create `docs/cli-json-schema.md`:
```markdown
# `bridge --json` schemas (Plan A)

All read commands accept `--json`. Output goes to stdout. Errors go to stderr as a single line: `{"error":"...","code":N}`.

## `bridge list --json`

Without `-r`: array of `Repo`:

```json
[
  {
    "name": "bridge",
    "path": "/home/me/projects/repos/github/me/public/bridge",
    "forge": "github",
    "owner": "me",
    "visibility": "public",
    "topics": [],
    "desc": "",
    "default_branch": "",
    "remote_url": "",
    "last_used": "0001-01-01T00:00:00Z"
  }
]
```

With `-r`:

```json
{
  "local": [ /* Repo[] as above */ ],
  "remote": [
    {
      "forge": "github",
      "owner": "me",
      "name": "bridge",
      "default_branch": "main",
      "description": "",
      "topics": [],
      "visibility": "public",
      "html_url": "https://github.com/me/bridge",
      "ssh_url": "git@github.com:me/bridge.git",
      "updated_at": "2026-05-01T00:00:00Z"
    }
  ]
}
```

## `bridge slots --json`

Array of `Slot`:

```json
[
  {
    "id": "bridge-main",
    "repo": "bridge",
    "worktree": "",
    "agent": "claude",
    "created": "2026-05-01T00:00:00Z"
  }
]
```

## `bridge sessions --json`

Array of `Session`:

```json
[
  {
    "slot_id": "bridge-main",
    "state": "attached",
    "age": 7200000000000,
    "pid": 0,
    "tmux_name": "bridge-main"
  }
]
```

`age` is `time.Duration` nanoseconds (Go default JSON encoding).

## `bridge presence --json`

```json
{
  "mode": "away",
  "overrides": {"slot-id": "on"},
  "updated_at": "2026-05-01T00:00:00Z"
}
```

## `bridge sync --json`

```json
{
  "last_run": "2026-05-01T00:00:00Z",
  "queue": ["repo-a", "repo-b"],
  "unpushed": ["owner/repo"]
}
```

## `bridge issues --json`

Array of `Issue`:

```json
[
  {
    "forge": "github",
    "repo": "me/bridge",
    "number": 30,
    "title": "feat: dashboard",
    "url": "https://...",
    "labels": ["area:tui"],
    "updated": "2026-05-01T00:00:00Z"
  }
]
```

## `bridge status --json`

```json
{
  "sessions": 0,
  "presence": "auto",
  "sync": {"unpushed": 0},
  "version": "bridge dev (commit none, built unknown)"
}
```

## Error shape

```json
{"error": "repo not found", "code": 2}
```

Codes: `1` internal/FS/subprocess; `2` user input; `3` network with no cache fallback.
```

- [ ] **Step 2: Add `install-go` Makefile target**

Append to `Makefile`:
```make
install-go: build-go
	install -m 0755 bridge-go $(HOME)/.local/bin/bridge-go
```

- [ ] **Step 3: Build and sanity-check the binary**

Run:
```bash
make build-go
./bridge-go --version
./bridge-go list --help
./bridge-go status --json
```

Expect: version prints, help renders, status emits valid JSON with default fields.

- [ ] **Step 4: Commit**

```bash
git add docs/cli-json-schema.md Makefile
git commit -m "docs(go): --json schema reference + install-go make target"
```

---

## Task 24: Wire `--help` polish + verify full CLI tree ✅

**Files:**
- Modify: `cmd/bridge/root.go`

- [ ] **Step 1: Add long descriptions**

Edit `cmd/bridge/root.go` to give the root command useful help text:
```go
var rootCmd = &cobra.Command{
    Use:     "bridge",
    Short:   "Repo picker + agent launcher (Go core)",
    Long: `bridge is a Go-native rewrite of the bash bridge tool.
Plan A (this binary) ships read-only commands alongside the existing bash binary;
interactive commands (open, rm, presence write, sync now, watch) ship in Plan B.

Cache lives at ~/.cache/bridge/ (overridable via XDG_CACHE_HOME).
Repo discovery walks ~/projects/repos/ (overridable via BRIDGE_REPOS_ROOT).`,
    Version: versionString(),
}
```

- [ ] **Step 2: Confirm `--help` shows all verbs**

Run:
```bash
make build-go
./bridge-go --help
```

Expect output to list: `issues`, `list`, `presence`, `sessions`, `slots`, `status`, `sync` (alphabetical).

- [ ] **Step 3: Run the full test suite**

```bash
go test ./...
```

Expect: all green.

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/root.go
git commit -m "chore(go): polish root --help text"
```

---

## Task 25: Cross-compile smoke for Windows + final smoke ✅

**Files:** none (verification only).

- [ ] **Step 1: Cross-compile Windows binary**

Run:
```bash
GOOS=windows GOARCH=amd64 go build -o /tmp/bridge.exe ./cmd/bridge
file /tmp/bridge.exe
```

Expect: `PE32+ executable (console) x86-64`.

- [ ] **Step 2: Confirm CI workflow passes locally**

Run the same commands the CI workflow does:
```bash
go build ./...
go test ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/bridge.exe ./cmd/bridge
```

All should succeed.

- [ ] **Step 3: Sanity check against bash cache**

If `~/.cache/bridge/mru` exists (you've been running the bash bridge), Plan A's `bridge-go list` should NOT touch it (Plan A is read-only on MRU; writes are Plan B). Confirm with:
```bash
stat -c '%Y' ~/.cache/bridge/mru
./bridge-go list >/dev/null
stat -c '%Y' ~/.cache/bridge/mru
```

Both timestamps should match.

- [ ] **Step 4: Tag the milestone (not pushed)**

```bash
git tag -a v2.0.0-go.0 -m "Plan A complete: Go binary ships read-only commands alongside bash"
```

(Don't push the tag yet — that's a deliberate user action after acceptance.)

- [ ] **Step 5: Final commit (CHANGELOG note deferred to Plan B)**

No additional commit needed; this task is verification only. The work is on the branch.

---

## Spec coverage check (self-review notes)

Mapping spec sections to plan tasks:

| Spec requirement | Task(s) |
|---|---|
| Go binary entrypoint, cobra | 1, 2, 24 |
| `internal/store` atomic IO + flock + schema | 3, 4, 5 |
| `internal/core.Repo` + discovery | 6 |
| `internal/core.MRU` + bash compat | 7, 22 |
| `internal/core.Slot` | 13 |
| `internal/core.Session` + tmux read | 15 |
| `internal/core.Presence` (read) | 17 |
| `internal/core.Issue` (alias) | 20 |
| `internal/forge.Client` + cache | 9 |
| GitHub client | 10 |
| GitLab + Forgejo clients | 11 |
| `bridge list` (local + `-r --refresh`) | 8, 12 |
| `bridge slots` | 14 |
| `bridge sessions` | 16 |
| `bridge presence` (read) | 18 |
| `bridge sync` (read) | 19 |
| `bridge issues` (fetch + cache) | 20 |
| `bridge status` (slim composed) | 21 |
| `bridge --version` | 2 |
| `--json` contract per command | 8, 12, 14, 16, 18, 19, 20, 21, 23 |
| Error shape JSON contract | doc'd in 23; runtime errors via cobra default |
| Cross-platform (Windows cross-compile) | 1 (CI), 4 (lock), 25 (smoke) |
| Soft-cutover with bash format | 22 (MRU compat smoke) |

**Out of Plan A (deferred to Plan B):**
- Interactive commands: `open`, `rm`, `presence away|back`, `sync now`, `sync --auto`, `watch`, `tui`
- `internal/launcher` (tmux launch, WT launch)
- `internal/agents` (claude/copilot/opencode spawn)
- `internal/shellbridge` (__preflight directive protocol)
- Shell shim (`bridge-shim.sh`, `bridge-shim.ps1`)
- Legacy flag silent-forwarding shim layer
- Phase 4 cleanup (removing bash scripts)
- CHANGELOG entry (lands with Plan B cutover, per spec's `_BRIDGE_VERSION` sunset rule)

**Structured logging via `log/slog`:** spec calls for it; Plan A's commands are short-lived and silent by default — adding `-v` plumbing is deferred to Plan B where long-running processes need it. Errors still surface via cobra's default mechanism. If a reviewer flags this as a Plan A gap, lift it forward; otherwise Plan B picks it up.
