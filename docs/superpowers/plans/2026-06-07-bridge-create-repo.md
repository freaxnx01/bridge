# bridge create-repo + bot /newrepo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `bridge create <name> --forge forgejo|github [--public]` (create + clone a repo) and a bot `/newrepo` command that picks forge × visibility then offers to launch a session.

**Architecture:** Go: each forge client gains `CreateRepo` (POST to the forge API); a new `cmd/bridge/create.go` resolves the per-forge target dir + token (existing `envFromDirenv`), calls the client, then `git clone`s into the conventional local layout. Python: `bridge-bot` adds `/newrepo` (2×2 inline keyboard → `bridge create … --json` → launch button).

**Tech Stack:** Go (cobra, net/http, httptest), Python 3 stdlib (`unittest`), git, tmux, bridge CLI.

---

## File structure

| File | Responsibility |
| --- | --- |
| `internal/forge/client.go` | add shared `ErrRepoExists` |
| `internal/forge/forgejo.go` | `post()` + `CreateRepo` (POST /api/v1/user/repos), 409→ErrRepoExists |
| `internal/forge/github.go` | `post()` + `CreateRepo` (POST /user/repos), 422→ErrRepoExists |
| `internal/forge/{forgejo,github}_test.go` | CreateRepo via httptest |
| `cmd/bridge/create.go` | `bridge create` command: resolve forge/vis→dir+token, create, clone (`cloneFn` seam) |
| `cmd/bridge/create_test.go` | name validation + end-to-end via temp root + httptest + stub clone |
| `bridge-bot/handlers.py` | `cmd_newrepo`, `newrepo:`/`newrepo_launch:` callbacks, `Context.repo_creator` |
| `bridge-bot/bridge_bot.py` | `_create_repo`, dispatch `/newrepo`, `BOT_COMMANDS` entry |
| `bridge-bot/tests/test_handlers.py` | keyboard, create callback, launch callback, usage |

Work from: `/home/freax/projects/repos/github/freaxnx01/public/bridge` (branch `control-bot`).

---

## Task 1: Forgejo `CreateRepo`

**Files:**
- Modify: `internal/forge/client.go` (add `ErrRepoExists`)
- Modify: `internal/forge/forgejo.go` (add `post`, `CreateRepo`, `Owner` on `fjRepo`)
- Test: `internal/forge/forgejo_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/forge/forgejo_test.go`:

```go
package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForgejoCreateRepo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/user/repos" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token T" {
			t.Fatalf("missing token auth: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","private":true,"default_branch":"main",
			"html_url":"https://git/h/foo","ssh_url":"ssh://git@git/h/foo.git",
			"owner":{"login":"freax"}}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	ref, err := c.CreateRepo(context.Background(), "foo", true)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["private"] != true || gotBody["auto_init"] != true {
		t.Fatalf("body = %v", gotBody)
	}
	if ref.Name != "foo" || ref.Owner != "freax" || ref.Visibility != "private" {
		t.Fatalf("ref = %+v", ref)
	}
	if ref.SSHURL == "" {
		t.Fatal("missing ssh_url")
	}
}

func TestForgejoCreateRepoConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()
	_, err := NewForgejoClient("T", srv.URL).CreateRepo(context.Background(), "foo", true)
	if !errors.Is(err, ErrRepoExists) {
		t.Fatalf("want ErrRepoExists, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/forge/ -run TestForgejoCreateRepo -v`
Expected: FAIL — `c.CreateRepo undefined` / `ErrRepoExists undefined`.

- [ ] **Step 3: Add `ErrRepoExists` to `internal/forge/client.go`**

Add the import and var (top-level, after the imports):

```go
import (
	"context"
	"errors"
	"time"
)

// ErrRepoExists is returned by CreateRepo when the repo already exists.
var ErrRepoExists = errors.New("repo already exists")
```

- [ ] **Step 4: Add `post` + `CreateRepo` to `internal/forge/forgejo.go`**

Add `"bytes"` to the import block. Add an `Owner` field to `fjRepo`:

```go
type fjRepo struct {
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	HTMLURL       string    `json:"html_url"`
	SSHURL        string    `json:"ssh_url"`
	UpdatedAt     time.Time `json:"updated_at"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}
```

Add after the `get` method:

```go
func (c *ForgejoClient) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(buf))
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return ErrRepoExists
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CreateRepo creates a repo under the authenticated user (auto-initialized).
func (c *ForgejoClient) CreateRepo(ctx context.Context, name string, private bool) (RepoRef, error) {
	body := map[string]any{
		"name": name, "private": private, "auto_init": true, "default_branch": "main",
	}
	var r fjRepo
	if err := c.post(ctx, "/api/v1/user/repos", body, &r); err != nil {
		return RepoRef{}, err
	}
	vis := "public"
	if r.Private {
		vis = "private"
	}
	return RepoRef{
		Forge: "forgejo", Owner: r.Owner.Login, Name: r.Name,
		DefaultBranch: r.DefaultBranch, Visibility: vis,
		HTMLURL: r.HTMLURL, SSHURL: r.SSHURL,
	}, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/forge/ -run TestForgejoCreateRepo -v`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add internal/forge/client.go internal/forge/forgejo.go internal/forge/forgejo_test.go
git commit -m "feat(forge): Forgejo CreateRepo"
```

---

## Task 2: GitHub `CreateRepo`

**Files:**
- Modify: `internal/forge/github.go` (add `post`, `CreateRepo`)
- Test: `internal/forge/github_test.go`

GitHub returns **422** (not 409) when a repo name already exists; map that to `ErrRepoExists`.

- [ ] **Step 1: Write the failing test**

Create `internal/forge/github_test.go`:

```go
package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGithubCreateRepo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/user/repos" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer T" {
			t.Fatalf("bad auth %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","visibility":"private","default_branch":"main",
			"html_url":"https://gh/freaxnx01/foo","ssh_url":"git@github.com:freaxnx01/foo.git",
			"owner":{"login":"freaxnx01"}}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	ref, err := c.CreateRepo(context.Background(), "foo", true)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["private"] != true || gotBody["auto_init"] != true {
		t.Fatalf("body = %v", gotBody)
	}
	if ref.Name != "foo" || ref.Owner != "freaxnx01" || ref.Visibility != "private" {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestGithubCreateRepoExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	_, err := NewGithubClient("T", srv.URL).CreateRepo(context.Background(), "foo", true)
	if !errors.Is(err, ErrRepoExists) {
		t.Fatalf("want ErrRepoExists, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/forge/ -run TestGithubCreateRepo -v`
Expected: FAIL — `c.CreateRepo undefined`.

- [ ] **Step 3: Add `post` + `CreateRepo` to `internal/forge/github.go`**

Add `"bytes"` to the import block. Add after the `get` method:

```go
func (c *GithubClient) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(buf))
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return ErrRepoExists
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CreateRepo creates a repo under the authenticated user (auto-initialized).
func (c *GithubClient) CreateRepo(ctx context.Context, name string, private bool) (RepoRef, error) {
	body := map[string]any{"name": name, "private": private, "auto_init": true}
	var r ghRepo
	if err := c.post(ctx, "/user/repos", body, &r); err != nil {
		return RepoRef{}, err
	}
	vis := r.Visibility
	if vis == "" {
		vis = "public"
	}
	return RepoRef{
		Forge: "github", Owner: r.Owner.Login, Name: r.Name,
		DefaultBranch: r.DefaultBranch, Visibility: vis,
		HTMLURL: r.HTMLURL, SSHURL: r.SSHURL,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/forge/ -run TestGithubCreateRepo -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(forge): GitHub CreateRepo"
```

---

## Task 3: `bridge create` command

**Files:**
- Create: `cmd/bridge/create.go`
- Test: `cmd/bridge/create_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/create_test.go`:

```go
package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestValidRepoName(t *testing.T) {
	ok := []string{"foo", "foo-bar", "foo_bar.baz", "A1"}
	bad := []string{"", "foo bar", "foo/bar", "foo;rm", "..", "foo$x"}
	for _, n := range ok {
		if !validRepoName(n) {
			t.Errorf("want valid: %q", n)
		}
	}
	for _, n := range bad {
		if validRepoName(n) {
			t.Errorf("want invalid: %q", n)
		}
	}
}

func TestCreateForgejoEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "git-forgejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","private":true,"ssh_url":"ssh://x/foo.git",
			"html_url":"https://h/foo","owner":{"login":"freax"}}`))
	}))
	defer srv.Close()

	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("BRIDGE_FORGEJO_API", srv.URL)
	t.Setenv("FORGEJO_TOKEN", "T")

	var gotURL, gotTarget string
	old := cloneFn
	cloneFn = func(sshURL, target string) error { gotURL, gotTarget = sshURL, target; return nil }
	defer func() { cloneFn = old }()

	cmd := newCreateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"foo", "--forge", "forgejo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotURL != "ssh://x/foo.git" {
		t.Fatalf("clone url = %q", gotURL)
	}
	wantTarget := filepath.Join(root, "git-forgejo", "foo")
	if gotTarget != wantTarget {
		t.Fatalf("clone target = %q want %q", gotTarget, wantTarget)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"forge": "forgejo"`)) {
		t.Fatalf("json out = %s", out.String())
	}
}

func TestCreateGithubPublicTargetDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "github", "freaxnx01", "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","visibility":"public","ssh_url":"git@gh:freaxnx01/foo.git",
			"html_url":"https://h/foo","owner":{"login":"freaxnx01"}}`))
	}))
	defer srv.Close()

	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "T")

	var gotTarget string
	old := cloneFn
	cloneFn = func(sshURL, target string) error { gotTarget = target; return nil }
	defer func() { cloneFn = old }()

	cmd := newCreateCmd()
	cmd.SetArgs([]string{"foo", "--forge", "github", "--public"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "github", "freaxnx01", "public", "foo")
	if gotTarget != want {
		t.Fatalf("target = %q want %q", gotTarget, want)
	}
}

func TestCreateRejectsBadForge(t *testing.T) {
	cmd := newCreateCmd()
	cmd.SetArgs([]string{"foo", "--forge", "bitbucket"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for unknown forge")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge/ -run TestCreate -v`
Expected: FAIL — `newCreateCmd`, `cloneFn`, `validRepoName` undefined.

- [ ] **Step 3: Write `cmd/bridge/create.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/forge"
)

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validRepoName(s string) bool {
	return s != ".." && s != "." && repoNameRe.MatchString(s)
}

// cloneFn is a seam so tests can stub the actual git clone.
var cloneFn = func(sshURL, target string) error {
	c := exec.Command("git", "clone", sshURL, target)
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

const githubOwner = "freaxnx01"

func init() {
	rootCmd.AddCommand(newCreateCmd())
}

func newCreateCmd() *cobra.Command {
	var forgeName string
	var public, asJSON bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new repo (Forgejo or GitHub) and clone it locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args[0], forgeName, public, asJSON)
		},
	}
	cmd.Flags().StringVar(&forgeName, "forge", "forgejo", "forge: forgejo|github")
	cmd.Flags().BoolVar(&public, "public", false, "create a public repo (default private)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

// forgejoTargetDir returns the local git-forgejo dir + FORGEJO_TOKEN.
func forgejoTargetDir() (dir, token string, err error) {
	for _, root := range reposRoots() {
		d := filepath.Join(root, "git-forgejo")
		if dirExists(d) {
			return d, envFromDirenv(d, []string{"FORGEJO_TOKEN"})["FORGEJO_TOKEN"], nil
		}
	}
	return "", "", fmt.Errorf("no git-forgejo dir under repos roots")
}

// githubTargetDir returns the local github/<owner>/<vis> dir + token.
func githubTargetDir(vis string) (dir, token string, err error) {
	for _, root := range reposRoots() {
		d := filepath.Join(root, "github", githubOwner, vis)
		if dirExists(d) {
			env := envFromDirenv(d, []string{"GH_TOKEN", "GITHUB_TOKEN"})
			tok := env["GH_TOKEN"]
			if tok == "" {
				tok = env["GITHUB_TOKEN"]
			}
			return d, tok, nil
		}
	}
	return "", "", fmt.Errorf("no github/%s/%s dir under repos roots", githubOwner, vis)
}

func runCreate(cmd *cobra.Command, name, forgeName string, public, asJSON bool) error {
	if !validRepoName(name) {
		return fmt.Errorf("invalid repo name %q (allowed: A-Za-z0-9._-)", name)
	}
	private := !public
	vis := "private"
	if public {
		vis = "public"
	}
	ctx := context.Background()

	var ref forge.RepoRef
	var targetDir string
	switch forgeName {
	case "forgejo":
		dir, tok, err := forgejoTargetDir()
		if err != nil {
			return err
		}
		if tok == "" {
			return fmt.Errorf("no Forgejo token (check %s/.envrc)", dir)
		}
		ref, err = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API")).CreateRepo(ctx, name, private)
		if err != nil {
			return createErr(err, name)
		}
		targetDir = filepath.Join(dir, name)
	case "github":
		dir, tok, err := githubTargetDir(vis)
		if err != nil {
			return err
		}
		if tok == "" {
			return fmt.Errorf("no GitHub token (check %s/.envrc)", dir)
		}
		ref, err = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API")).CreateRepo(ctx, name, private)
		if err != nil {
			return createErr(err, name)
		}
		targetDir = filepath.Join(dir, name)
	default:
		return fmt.Errorf("unknown forge %q (use forgejo or github)", forgeName)
	}

	if err := cloneFn(ref.SSHURL, targetDir); err != nil {
		return fmt.Errorf("created %s but clone failed: %v\nclone manually: git clone %s %s",
			ref.HTMLURL, err, ref.SSHURL, targetDir)
	}

	if asJSON {
		return emitJSON(cmd.OutOrStdout(), map[string]any{
			"name": ref.Name, "full_name": ref.Owner + "/" + ref.Name,
			"forge": forgeName, "private": private, "path": targetDir, "html_url": ref.HTMLURL,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✅ created %s/%s (%s, %s) → %s\n",
		ref.Owner, ref.Name, vis, forgeName, targetDir)
	return nil
}

func createErr(err error, name string) error {
	if errors.Is(err, forge.ErrRepoExists) {
		return fmt.Errorf("repo %q already exists", name)
	}
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/bridge/ -run TestCreate -v && go test ./cmd/bridge/ -run TestValidRepoName -v`
Expected: PASS (all create tests).

- [ ] **Step 5: Build + full Go suite**

Run: `go build ./... && go test ./...`
Expected: build succeeds; all packages PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/create.go cmd/bridge/create_test.go
git commit -m "feat(bridge): create command (forgejo|github, private|public) + clone"
```

---

## Task 4: bot `/newrepo` handlers

**Files:**
- Modify: `bridge-bot/handlers.py`
- Test: `bridge-bot/tests/test_handlers.py`

- [ ] **Step 1: Write the failing test** — edit `bridge-bot/tests/test_handlers.py`

The file already has a `FakeBot` (records `sent`/`edited`, each with `reply_markup`) and a single `make_ctx(items=...)` builder. First update `make_ctx` to pass a default `repo_creator` (required once we add the field) — add this line inside its `handlers.Context(...)` call, after `status_provider=...`:

```python
        repo_creator=lambda name, forge, private: {
            "name": name, "full_name": f"o/{name}", "forge": forge,
            "private": private, "path": f"/r/{name}", "html_url": "u"},
```

Then add this test class at the end of the file (it uses `make_ctx()` and the real `FakeBot` attributes `sent`/`edited` with `reply_markup`; `Context` is a mutable dataclass so tests override fields by assignment):

```python
class NewRepoTests(unittest.TestCase):
    def test_newrepo_shows_forge_visibility_keyboard(self):
        ctx = make_ctx()
        handlers.cmd_newrepo(ctx, 7, "myproj")
        kb = ctx.bot.sent[-1]["reply_markup"]["inline_keyboard"]
        datas = [b["callback_data"] for row in kb for b in row]
        self.assertIn("newrepo:forgejo:private:myproj", datas)
        self.assertIn("newrepo:forgejo:public:myproj", datas)
        self.assertIn("newrepo:github:private:myproj", datas)
        self.assertIn("newrepo:github:public:myproj", datas)

    def test_newrepo_empty_name_usage(self):
        ctx = make_ctx()
        handlers.cmd_newrepo(ctx, 7, "")
        self.assertIn("Usage", ctx.bot.sent[-1]["text"])

    def test_newrepo_invalid_name_rejected(self):
        ctx = make_ctx()
        handlers.cmd_newrepo(ctx, 7, "bad name")
        self.assertIn("invalid", ctx.bot.sent[-1]["text"].lower())

    def test_create_callback_invokes_creator_and_offers_launch(self):
        ctx = make_ctx()
        seen = {}
        ctx.repo_creator = lambda name, forge, private: (
            seen.update(name=name, forge=forge, private=private)
            or {"name": name, "full_name": f"o/{name}", "forge": forge,
                "private": private, "path": "/r/x", "html_url": "u"})
        handlers.on_callback(ctx, chat_id=7, callback_id="c",
                             data="newrepo:github:public:myproj", message_id=5)
        self.assertEqual(seen, {"name": "myproj", "forge": "github", "private": False})
        kb = ctx.bot.edited[-1]["reply_markup"]["inline_keyboard"]
        datas = [b["callback_data"] for row in kb for b in row]
        self.assertIn("newrepo_launch:myproj", datas)

    def test_create_callback_failure_reports_error(self):
        ctx = make_ctx()
        ctx.repo_creator = lambda *a: None
        handlers.on_callback(ctx, chat_id=7, callback_id="c",
                             data="newrepo:forgejo:private:myproj", message_id=5)
        self.assertIn("failed", ctx.bot.edited[-1]["text"].lower())

    def test_launch_callback_spawns(self):
        ctx = make_ctx()
        seen = {}
        ctx.spawner = lambda name, extra: (
            seen.update(name=name) or {"slot": name, "session": name})
        handlers.on_callback(ctx, chat_id=7, callback_id="c",
                             data="newrepo_launch:myproj", message_id=5)
        self.assertEqual(seen["name"], "myproj")
        self.assertIn("Launched", ctx.bot.edited[-1]["text"])
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd bridge-bot && python3 -m unittest tests.test_handlers -v`
Expected: FAIL — `cmd_newrepo` missing / `repo_creator` not a Context field.

- [ ] **Step 3: Implement in `bridge-bot/handlers.py`**

Add `import re` at the top. Add `repo_creator` to the `Context` dataclass (after `status_provider`, the last existing field):

```python
    repo_creator: Callable[[str, str, bool], dict | None]
```

Add the name validator and command + callbacks:

```python
_REPO_NAME_RE = re.compile(r"^[A-Za-z0-9._-]+$")


def cmd_newrepo(ctx: Context, chat_id: int, args: str) -> None:
    name = args.strip().split()[0] if args.strip() else ""
    if not name:
        ctx.bot.send_message(chat_id, "Usage: /newrepo <name>")
        return
    if not _REPO_NAME_RE.match(name):
        ctx.bot.send_message(chat_id, "Invalid name (allowed: A-Za-z0-9._-)")
        return
    ctx.bot.send_message(
        chat_id, f'Create "{name}" where?',
        reply_markup={"inline_keyboard": [
            [{"text": "Forgejo · Private", "callback_data": f"newrepo:forgejo:private:{name}"},
             {"text": "Forgejo · Public", "callback_data": f"newrepo:forgejo:public:{name}"}],
            [{"text": "GitHub · Private", "callback_data": f"newrepo:github:private:{name}"},
             {"text": "GitHub · Public", "callback_data": f"newrepo:github:public:{name}"}],
        ]},
    )
```

In `on_callback`, add these branches at the top (before the picker-state lookup), mirroring the existing `stop_confirm:` style:

```python
    if data.startswith("newrepo:"):
        _, forge, vis, name = data.split(":", 3)
        ctx.bot.answer_callback_query(callback_id, f"Creating {name}…")
        result = ctx.repo_creator(name, forge, vis == "private")
        if result:
            ctx.bot.edit_message_text(
                chat_id, message_id,
                f"✅ Created + cloned: {result['full_name']} ({forge})",
                reply_markup={"inline_keyboard": [[
                    {"text": "🚀 Launch session", "callback_data": f"newrepo_launch:{name}"}]]},
            )
        else:
            ctx.bot.edit_message_text(
                chat_id, message_id, f"❌ Create failed for {name}",
                reply_markup={"inline_keyboard": []})
        return
    if data.startswith("newrepo_launch:"):
        name = data.split(":", 1)[1]
        ctx.bot.answer_callback_query(callback_id, f"Launching {name}…")
        result = ctx.spawner(name, [])
        if result:
            text = f"✅ Launched: {name} → {result['slot']} (tmux: {result['session']})"
        else:
            text = "⏳ Spawn dispatched. Check /status in a few seconds."
        ctx.bot.edit_message_text(chat_id, message_id, text, reply_markup={"inline_keyboard": []})
        return
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd bridge-bot && python3 -m unittest tests.test_handlers -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add bridge-bot/handlers.py bridge-bot/tests/test_handlers.py
git commit -m "feat(bridge-bot): /newrepo handlers (forge×visibility + launch)"
```

---

## Task 5: bot entrypoint wiring

**Files:**
- Modify: `bridge-bot/bridge_bot.py`
- Test: `bridge-bot/tests/test_status.py` (extend) + `bridge-bot/tests/test_kill.py` (creator)

- [ ] **Step 1: Write the failing test** — append to `bridge-bot/tests/test_kill.py`

```python
class CreateRepoTests(unittest.TestCase):
    def test_create_repo_parses_json(self):
        payload = '{"name":"foo","full_name":"o/foo","forge":"forgejo","private":true,"path":"/r/foo"}'
        with mock.patch.object(bridge_bot.subprocess, "run",
                               return_value=mock.Mock(stdout=payload, returncode=0)):
            out = bridge_bot._create_repo("foo", "forgejo", True)
        self.assertEqual(out["full_name"], "o/foo")

    def test_create_repo_command_shape(self):
        seen = {}
        def fake(cmd, **kw):
            seen["cmd"] = cmd
            return mock.Mock(stdout='{"name":"foo"}', returncode=0)
        with mock.patch.object(bridge_bot.subprocess, "run", side_effect=fake):
            bridge_bot._create_repo("foo", "github", False)
        self.assertEqual(seen["cmd"][:3], ["bridge", "create", "foo"])
        self.assertIn("--forge", seen["cmd"])
        self.assertIn("github", seen["cmd"])
        self.assertIn("--public", seen["cmd"])  # private=False → --public
        self.assertIn("--json", seen["cmd"])

    def test_create_repo_failure_returns_none(self):
        with mock.patch.object(bridge_bot.subprocess, "run",
                               return_value=mock.Mock(stdout="not json", returncode=1)):
            self.assertIsNone(bridge_bot._create_repo("foo", "forgejo", True))
```

Also add to `bridge-bot/tests/test_status.py` `test_bot_commands_match_dispatcher` the new command — change the expected set to include `newrepo`:

```python
        self.assertEqual(advertised,
                         {"new", "newrepo", "status", "kill", "cancel", "help"})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd bridge-bot && python3 -m unittest tests.test_kill tests.test_status -v`
Expected: FAIL — `_create_repo` missing; `newrepo` not in `BOT_COMMANDS`.

- [ ] **Step 3: Implement in `bridge-bot/bridge_bot.py`**

Add `_create_repo` near the other provider helpers (after `_spawn_and_confirm`):

```python
def _create_repo(name: str, forge: str, private: bool) -> dict | None:
    cmd = ["bridge", "create", name, "--forge", forge, "--json"]
    if not private:
        cmd.append("--public")
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=60,
                             env=spawn.clean_env())
        if out.returncode != 0:
            _log_event(evt="create_repo", name=name, ok=False, err=out.stderr.strip())
            return None
        return json.loads(out.stdout)
    except (subprocess.SubprocessError, json.JSONDecodeError, ValueError) as e:
        _log_event(evt="create_repo", name=name, ok=False, err=str(e))
        return None
```

Wire it into `build_context` (add the kwarg alongside the others):

```python
        repo_creator=_create_repo,
```

Add `newrepo` to `BOT_COMMANDS` (after the `new` entry):

```python
    {"command": "newrepo", "description": "Create a new repo (Forgejo/GitHub)"},
```

Dispatch in `_handle_message` (add after the `new` branch):

```python
    elif cmd == "newrepo":
        handlers.cmd_newrepo(ctx, chat_id, rest.strip())
```

(Note: `rest` is from `head, _, rest = text.partition(" ")` — confirm the dispatcher computes `rest`; it does in the `new` branch.)

- [ ] **Step 4: Run test to verify it passes + full suite**

Run: `cd bridge-bot && python3 -m unittest discover tests -v`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
git add bridge-bot/bridge_bot.py bridge-bot/tests/test_kill.py bridge-bot/tests/test_status.py
git commit -m "feat(bridge-bot): wire /newrepo + bridge create shell-out"
```

---

## Task 6: Deploy + end-to-end verification

**Files:** none (operational)

- [ ] **Step 1: Install the rebuilt bridge binary**

Run:
```bash
cd ~/projects/repos/github/freaxnx01/public/bridge
go build -o ~/.local/bin/bridge ./cmd/bridge
bridge create --help | head -5   # expect the create usage
```
Expected: `bridge create <name>` help shows `--forge`, `--public`, `--json`.

- [ ] **Step 2: CLI smoke test (real Forgejo, throwaway name)**

Run:
```bash
bridge create bridge-bot-smoke --forge forgejo --json
ls -d ~/repos/git-forgejo/bridge-bot-smoke/.git && echo "cloned OK"
```
Expected: JSON with `"forge":"forgejo","private":true`; the clone dir exists.
Cleanup after verifying: delete the repo in Forgejo UI + `rm -rf ~/repos/git-forgejo/bridge-bot-smoke`.

- [ ] **Step 3: Restart the bot to load new code + register /newrepo**

Run:
```bash
sudo systemctl restart bridge-bot
sleep 3
systemctl is-active bridge-bot
grep -E '"evt": "(commands_registered|commands_error)"' ~/.cache/bridge/bridge-bot.log | tail -1
```
Expected: `active`; `commands_registered n=6`.

- [ ] **Step 4: Live test from phone**

DM `@agent_dev_ctl_bot`: type `/` → confirm `/newrepo` appears in the menu. Send `/newrepo testbot` → tap **Forgejo · Private** → expect "✅ Created + cloned" + a **🚀 Launch session** button → tap it → expect a launched session. Verify on host:
```bash
ls -d ~/repos/git-forgejo/testbot/.git && bridge sessions --json | grep testbot
```
Cleanup the throwaway `testbot` repo afterward if desired.

- [ ] **Step 5: Confirm clean tree**

```bash
cd ~/projects/repos/github/freaxnx01/public/bridge && git status --short
```
Expected: clean (binary install + created repos are not repo files).

---

## Notes for the implementer

- `RepoRef` already has `Owner`, `Name`, `Visibility`, `HTMLURL`, `SSHURL` (in `internal/forge/client.go`) — do not redefine it.
- `envFromDirenv(dir, vars)` falls back to process env when direnv is absent, which is why tests set `FORGEJO_TOKEN`/`GH_TOKEN` directly.
- Clone always uses `ref.SSHURL` (matches the existing SSH remotes on `:222` for Forgejo).
- `cloneFn` is the only seam for git; keep all git-clone behavior behind it so tests never hit the network or disk clone.
- Spawner contract unchanged: `spawner(name, extra) -> {"slot","session",...}`; `newrepo_launch` passes `extra=[]`.
