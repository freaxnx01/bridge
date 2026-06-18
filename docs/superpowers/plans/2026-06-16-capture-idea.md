# Idea Capture (#2a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture an idea from mobile — `/idea <text>` in bridge-bot → tap a target → committed to GitHub (ideas-lab dated file or a repo's `ideas.md`) via the Contents API, with a confirmation link.

**Architecture:** A headless Go capture core (`internal/capture` + forge Contents-API `GetFile`/`PutFile`) does the write; `bridge capture idea` is the CLI; bridge-bot is a thin client (text-first `/idea` → target picker → shell out). Tokens resolve per-owner via a new `internal/remote` helper. No local checkout is touched.

**Tech Stack:** Go (stdlib `net/http`/`encoding/base64`/`testing`/`httptest`), Cobra, Python 3 (bridge-bot, stdlib `unittest`). Spec: `docs/superpowers/specs/2026-06-16-capture-idea-design.md`.

**Resolved open questions:** token via per-owner direnv helper `remote.GitHubToken` (no new token env); ideas-lab repo identity from new `BRIDGE_IDEAS_LAB_REPO` env (`owner/name`). The CLI `--target` accepts `ideas-lab` or a **repo name** and resolves the owner itself (reusing repo discovery), so the bot passes only what it has.

---

## File Structure

- **Modify** `internal/forge/github.go` — `GetFile`, `PutFile` (Contents API).
- **Modify** `internal/forge/github_test.go` — stub tests.
- **Create** `internal/capture/capture.go` — `Target`, `FileWriter`, `CaptureIdea`, slug.
- **Create** `internal/capture/capture_test.go` — fake-writer tests.
- **Modify** `internal/remote/remote.go` — exported `GitHubToken(roots, owner)`.
- **Modify** `internal/remote/remote_test.go` — token-helper test.
- **Create** `cmd/bridge/capture.go` — `capture` group + `capture idea`.
- **Create** `cmd/bridge/capture_test.go` — target-resolution/parse tests.
- **Modify** `bridge-bot/handlers.py` — `Context` fields, `cmd_idea`, `on_callback` idea branch, help.
- **Modify** `bridge-bot/bridge_bot.py` — dispatch `/idea` + wire `capture_idea`/config.
- **Modify** `bridge-bot/tests/test_handlers.py` — `/idea` flow tests.

---

## Task 1: forge Contents API (`GetFile`/`PutFile`)

**Files:**
- Modify: `internal/forge/github.go`
- Test: `internal/forge/github_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/forge/github_test.go`:

```go
func TestGithubGetFile_FoundAndAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/freaxnx01/bridge/contents/ideas.md" {
			w.Header().Set("Content-Type", "application/json")
			// base64 of "# Ideas\n\n- one\n" (with a newline in the b64, as GitHub returns)
			w.Write([]byte(`{"sha":"abc123","html_url":"https://x/ideas.md","content":"IyBJZGVhcwoKLSBvbmUK\n"}`))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	content, sha, found, err := c.GetFile(context.Background(), "freaxnx01", "bridge", "ideas.md")
	if err != nil || !found {
		t.Fatalf("GetFile: found=%v err=%v", found, err)
	}
	if sha != "abc123" || string(content) != "# Ideas\n\n- one\n" {
		t.Errorf("got sha=%q content=%q", sha, string(content))
	}

	_, _, found, err = c.GetFile(context.Background(), "freaxnx01", "bridge", "missing.md")
	if err != nil || found {
		t.Errorf("absent file: found=%v err=%v (want found=false, nil err)", found, err)
	}
}

func TestGithubPutFile_CreateAndUpdate(t *testing.T) {
	var gotBodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method: %s", r.Method)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotBodies = append(gotBodies, body)
		w.Write([]byte(`{"content":{"html_url":"https://x/created.md"}}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	url, err := c.PutFile(context.Background(), "freaxnx01", "ideas-lab", "ideas/2026-06-16-x.md", []byte("hi"), "capture: x", "")
	if err != nil || url != "https://x/created.md" {
		t.Fatalf("PutFile create: url=%q err=%v", url, err)
	}
	if _, hasSHA := gotBodies[0]["sha"]; hasSHA {
		t.Errorf("create must not send sha: %v", gotBodies[0])
	}
	if gotBodies[0]["content"] != "aGk=" { // base64("hi")
		t.Errorf("content not base64: %v", gotBodies[0]["content"])
	}

	_, err = c.PutFile(context.Background(), "freaxnx01", "bridge", "ideas.md", []byte("x"), "capture: idea", "abc123")
	if err != nil {
		t.Fatalf("PutFile update: %v", err)
	}
	if gotBodies[1]["sha"] != "abc123" {
		t.Errorf("update must send sha, got: %v", gotBodies[1])
	}
}
```

`github_test.go` already imports `context`, `net/http`, `net/http/httptest`, `testing`; add `"encoding/json"` if not present.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/forge/ -run 'TestGithubGetFile|TestGithubPutFile' -v`
Expected: FAIL — `GetFile`/`PutFile` undefined.

- [ ] **Step 3: Implement**

Add to `internal/forge/github.go` (it already imports `bytes`, `context`, `encoding/json`, `fmt`, `io`, `net/http`; add `"encoding/base64"` and `"strings"` if missing):

```go
// GetFile fetches a file's decoded content and blob sha via the Contents API.
// found is false (with nil error) when the file does not exist (404).
func (c *GithubClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", false, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", false, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", false, fmt.Errorf("github get %s: %s: %s", path, resp.Status, string(body))
	}
	var gc struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gc); err != nil {
		return nil, "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(gc.Content, "\n", ""))
	if err != nil {
		return nil, "", false, fmt.Errorf("decode %s: %w", path, err)
	}
	return raw, gc.SHA, true, nil
}

// PutFile creates or updates a file via the Contents API. Empty sha creates;
// a blob sha updates. Returns the file's html_url.
func (c *GithubClient) PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (string, error) {
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	if sha != "" {
		body["sha"] = sha
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github put %s: %s: %s", path, resp.Status, string(b))
	}
	var out struct {
		Content struct {
			HTMLURL string `json:"html_url"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Content.HTMLURL, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/forge/ -run 'TestGithubGetFile|TestGithubPutFile' -v`
Expected: PASS (both).

- [ ] **Step 5: Format/vet + commit**

Run: `gofmt -l internal/forge/ && go vet ./internal/forge/`

```bash
git add internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(forge): GitHub Contents API GetFile/PutFile

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `internal/capture` core

**Files:**
- Create: `internal/capture/capture.go`
- Test: `internal/capture/capture_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/capture/capture_test.go`:

```go
package capture

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeWriter records PutFile calls and serves canned GetFile responses.
type fakeWriter struct {
	files map[string]struct {
		content []byte
		sha     string
	}
	puts []struct {
		owner, repo, path, message, sha string
		content                         []byte
	}
}

func (f *fakeWriter) GetFile(_ context.Context, owner, repo, path string) ([]byte, string, bool, error) {
	if f.files == nil {
		return nil, "", false, nil
	}
	v, ok := f.files[owner+"/"+repo+"/"+path]
	if !ok {
		return nil, "", false, nil
	}
	return v.content, v.sha, true, nil
}

func (f *fakeWriter) PutFile(_ context.Context, owner, repo, path string, content []byte, message, sha string) (string, error) {
	f.puts = append(f.puts, struct {
		owner, repo, path, message, sha string
		content                         []byte
	}{owner, repo, path, message, sha, content})
	return "https://example/" + path, nil
}

var fixedNow = time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)

func TestCaptureIdea_IdeasLab_NewDatedFile(t *testing.T) {
	w := &fakeWriter{}
	_, err := CaptureIdea(context.Background(), w, Target{IdeasLab: true, Owner: "freaxnx01", Repo: "ideas-lab"}, "Kanban for issues!", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	p := w.puts[0]
	if p.path != "ideas/2026-06-16-kanban-for-issues.md" {
		t.Errorf("path = %q", p.path)
	}
	if p.sha != "" {
		t.Errorf("ideas-lab create must send empty sha, got %q", p.sha)
	}
	body := string(p.content)
	if !strings.Contains(body, "Status: seed") || !strings.Contains(body, "Captured: 2026-06-16 (Telegram capture)") || !strings.Contains(body, "Kanban for issues!") {
		t.Errorf("body missing preamble/text:\n%s", body)
	}
}

func TestCaptureIdea_Repo_AppendsToExistingIdeas(t *testing.T) {
	w := &fakeWriter{files: map[string]struct {
		content []byte
		sha     string
	}{
		"freaxnx01/bridge/ideas.md": {content: []byte("# Ideas\n\n- one\n"), sha: "s1"},
	}}
	_, err := CaptureIdea(context.Background(), w, Target{Owner: "freaxnx01", Repo: "bridge"}, "two", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	p := w.puts[0]
	if p.path != "ideas.md" || p.sha != "s1" {
		t.Errorf("path=%q sha=%q", p.path, p.sha)
	}
	if string(p.content) != "# Ideas\n\n- one\n- two\n" {
		t.Errorf("append wrong:\n%q", string(p.content))
	}
}

func TestCaptureIdea_Repo_CreatesWhenAbsent(t *testing.T) {
	w := &fakeWriter{}
	_, err := CaptureIdea(context.Background(), w, Target{Owner: "freaxnx01", Repo: "bridge"}, "first", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	p := w.puts[0]
	if p.sha != "" || string(p.content) != "# Ideas\n\n- first\n" {
		t.Errorf("create wrong: sha=%q content=%q", p.sha, string(p.content))
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Kanban for issues!", "kanban-for-issues"},
		{"  multiple   spaces  ", "multiple-spaces"},
		{"", "idea"},
		{"!!!", "idea"},
	}
	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/capture/ -v`
Expected: FAIL — package has no Go files / undefined `CaptureIdea`.

- [ ] **Step 3: Implement**

Create `internal/capture/capture.go`:

```go
// Package capture writes captured ideas to a Git-backed destination via an
// injected FileWriter (the GitHub Contents API in production). It is
// forge-token-free: callers supply the writer and the resolved repo/token.
package capture

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Target is where a captured idea lands: the ideas-lab repo (a new dated file)
// or a specific repo (appended to its ideas.md).
type Target struct {
	IdeasLab bool
	Owner    string
	Repo     string
}

// FileWriter is the subset of the forge client capture needs (consumer iface).
type FileWriter interface {
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
	PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (htmlURL string, err error)
}

// CaptureIdea writes text to the target and returns the file's URL.
func CaptureIdea(ctx context.Context, w FileWriter, t Target, text string, now time.Time) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty idea text")
	}
	if t.IdeasLab {
		path := fmt.Sprintf("ideas/%s-%s.md", now.Format("2006-01-02"), slug(text))
		body := fmt.Sprintf("Status: seed\nCaptured: %s (Telegram capture)\n\n%s\n", now.Format("2006-01-02"), text)
		return w.PutFile(ctx, t.Owner, t.Repo, path, []byte(body), "capture: "+slug(text), "")
	}
	const path = "ideas.md"
	existing, sha, found, err := w.GetFile(ctx, t.Owner, t.Repo, path)
	if err != nil {
		return "", err
	}
	var content string
	if !found || len(existing) == 0 {
		content = "# Ideas\n\n- " + text + "\n"
		sha = ""
	} else {
		content = string(existing)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "- " + text + "\n"
	}
	return w.PutFile(ctx, t.Owner, t.Repo, path, []byte(content), "capture: idea", sha)
}

// slug turns idea text into a filename-safe slug (lowercase, non-alnum -> "-",
// collapsed, trimmed, <=50 chars). Empty/punctuation-only -> "idea".
func slug(text string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	if s == "" {
		return "idea"
	}
	return s
}
```

(YAGNI: same-day-slug collision suffixing from the spec is omitted in v1 — same text + same day is a rare personal case; a second capture would overwrite the first. If this bites, add a `GetFile` probe + `-2` suffix. Noted as a follow-up, not built.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/capture/ -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/capture/
git commit -m "feat(capture): CaptureIdea core (ideas-lab file + repo ideas.md append)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: per-owner GitHub token helper

**Files:**
- Modify: `internal/remote/remote.go`
- Test: `internal/remote/remote_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/remote/remote_test.go` (it already has `mustMkdirEnvrc` from earlier work):

```go
func TestGitHubToken_ResolvesOwnerScope(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "freaxnx01"))
	t.Setenv("GH_TOKEN", "tok-abc")

	tok, ok := GitHubToken([]string{root}, "freaxnx01")
	if !ok || tok != "tok-abc" {
		t.Errorf("GitHubToken = %q,%v, want tok-abc,true", tok, ok)
	}

	if _, ok := GitHubToken([]string{root}, "nobody"); ok {
		t.Errorf("unknown owner should not resolve")
	}
}
```

(`envFromDirenv` falls back to the process env when `direnv` is absent, so `t.Setenv("GH_TOKEN", …)` drives the test deterministically.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/remote/ -run TestGitHubToken -v`
Expected: FAIL — `GitHubToken` undefined.

- [ ] **Step 3: Implement**

Add to `internal/remote/remote.go`:

```go
// GitHubToken resolves the GitHub token for owner from its .envrc scope across
// roots (the same per-owner discovery Refresh uses). Returns ok=false when no
// github target for that owner is found or the token is empty.
func GitHubToken(roots []string, owner string) (string, bool) {
	for _, root := range roots {
		for _, t := range discoverRemoteTargets(root) {
			if t.Forge != "github" || t.Owner != owner {
				continue
			}
			env := envFromDirenv(t.Dir, []string{"GH_TOKEN", "GITHUB_TOKEN"})
			tok := env["GH_TOKEN"]
			if tok == "" {
				tok = env["GITHUB_TOKEN"]
			}
			if tok != "" {
				return tok, true
			}
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/remote/ -run TestGitHubToken -v && go test ./internal/remote/`
Expected: PASS (new test + full package).

- [ ] **Step 5: Commit**

```bash
git add internal/remote/remote.go internal/remote/remote_test.go
git commit -m "feat(remote): GitHubToken per-owner direnv resolver

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `bridge capture idea` CLI

**Files:**
- Create: `cmd/bridge/capture.go`
- Test: `cmd/bridge/capture_test.go`

- [ ] **Step 1: Write the failing test (target resolution)**

Create `cmd/bridge/capture_test.go`:

```go
package main

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestResolveCaptureTarget(t *testing.T) {
	repos := []core.Repo{
		{Owner: "freaxnx01", Name: "bridge", Forge: "github"},
		{Owner: "freaxnx01", Name: "agent-os", Forge: "github"},
	}
	// ideas-lab literal
	tg, err := resolveCaptureTarget("ideas-lab", "freaxnx01/ideas-lab", repos)
	if err != nil || !tg.IdeasLab || tg.Owner != "freaxnx01" || tg.Repo != "ideas-lab" {
		t.Fatalf("ideas-lab: %+v err=%v", tg, err)
	}
	// repo by bare name
	tg, err = resolveCaptureTarget("bridge", "", repos)
	if err != nil || tg.IdeasLab || tg.Owner != "freaxnx01" || tg.Repo != "bridge" {
		t.Fatalf("bridge: %+v err=%v", tg, err)
	}
	// explicit owner/name
	tg, err = resolveCaptureTarget("freaxnx01/agent-os", "", repos)
	if err != nil || tg.Owner != "freaxnx01" || tg.Repo != "agent-os" {
		t.Fatalf("owner/name: %+v err=%v", tg, err)
	}
	// unknown
	if _, err := resolveCaptureTarget("nope", "", repos); err == nil {
		t.Errorf("unknown repo should error")
	}
	// ideas-lab requested but unconfigured
	if _, err := resolveCaptureTarget("ideas-lab", "", repos); err == nil {
		t.Errorf("ideas-lab without BRIDGE_IDEAS_LAB_REPO should error")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/bridge/ -run TestResolveCaptureTarget -v`
Expected: FAIL — `resolveCaptureTarget` undefined.

- [ ] **Step 3: Implement the command + resolver**

Create `cmd/bridge/capture.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/capture"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/remote"
)

var captureTarget string

var captureCmd = &cobra.Command{
	Use:   "capture",
	Short: "Capture ideas/issues/roadmap items into Git-backed destinations",
}

var captureIdeaCmd = &cobra.Command{
	Use:   "idea",
	Short: "Capture an idea (text from stdin) to a repo's ideas.md or ideas-lab",
	RunE:  runCaptureIdea,
}

func init() {
	captureIdeaCmd.Flags().StringVar(&captureTarget, "target", "", "ideas-lab | <repo-name> | <owner>/<name>")
	captureIdeaCmd.MarkFlagRequired("target")
	captureCmd.AddCommand(captureIdeaCmd)
	rootCmd.AddCommand(captureCmd)
}

// resolveCaptureTarget maps a --target string to a capture.Target. "ideas-lab"
// uses ideasLabRepo ("owner/name", from config). Otherwise the string is a repo
// identifier: "owner/name" is taken literally; a bare name is matched (case-
// insensitively) against the discovered repos (error on none/ambiguous).
func resolveCaptureTarget(target, ideasLabRepo string, repos []core.Repo) (capture.Target, error) {
	if target == "ideas-lab" {
		owner, name, ok := strings.Cut(ideasLabRepo, "/")
		if !ok || owner == "" || name == "" {
			return capture.Target{}, fmt.Errorf("ideas-lab target requires BRIDGE_IDEAS_LAB_REPO=owner/name")
		}
		return capture.Target{IdeasLab: true, Owner: owner, Repo: name}, nil
	}
	if owner, name, ok := strings.Cut(target, "/"); ok {
		return capture.Target{Owner: owner, Repo: name}, nil
	}
	var match *core.Repo
	for i := range repos {
		if strings.EqualFold(repos[i].Name, target) && repos[i].Forge == "github" {
			if match != nil {
				return capture.Target{}, fmt.Errorf("repo %q is ambiguous; use owner/name", target)
			}
			match = &repos[i]
		}
	}
	if match == nil {
		return capture.Target{}, fmt.Errorf("no github repo named %q", target)
	}
	return capture.Target{Owner: match.Owner, Repo: match.Name}, nil
}

func runCaptureIdea(cmd *cobra.Command, args []string) error {
	repos, _ := discoverAllRoots()
	tgt, err := resolveCaptureTarget(captureTarget, os.Getenv("BRIDGE_IDEAS_LAB_REPO"), repos)
	if err != nil {
		return err
	}
	textBytes, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read idea text: %w", err)
	}
	text := strings.TrimSpace(string(textBytes))
	if text == "" {
		return fmt.Errorf("no idea text on stdin")
	}
	tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
	if !ok {
		return fmt.Errorf("no github token for owner %q (need an .envrc GH_TOKEN with repo scope)", tgt.Owner)
	}
	client := forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
	url, err := capture.CaptureIdea(context.Background(), client, tgt, text, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), url)
	return nil
}
```

Add `"time"` to the import block (used by `time.Now()`).

- [ ] **Step 4: Run to verify pass + build**

Run: `go test ./cmd/bridge/ -run TestResolveCaptureTarget -v && go build ./...`
Expected: PASS; builds.

- [ ] **Step 5: Format/vet + commit**

Run: `gofmt -l cmd/bridge/ | grep -v worktrees && go vet ./cmd/bridge/`

```bash
git add cmd/bridge/capture.go cmd/bridge/capture_test.go
git commit -m "feat(bridge): capture idea CLI (stdin text, target resolution)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: bridge-bot `/idea`

**Files:**
- Modify: `bridge-bot/handlers.py`
- Modify: `bridge-bot/bridge_bot.py`
- Test: `bridge-bot/tests/test_handlers.py`

- [ ] **Step 1: Write the failing tests**

Append to `bridge-bot/tests/test_handlers.py` (extend `make_ctx` to inject the new fields, then add tests). First update `make_ctx`:

```python
def make_ctx(items=("foo", "bar", "baz")):
    return handlers.Context(
        bot=FakeBot(),
        pickers={},
        local_provider=lambda: list(items),
        remote_provider=lambda: [],
        mru_provider=lambda: list(items),
        spawner=lambda name, extra: {"slot": "2", "session": name},
        kill_session=lambda s: True,
        status_provider=lambda: "status output",
        idea_pending={},
        capture_idea=lambda target, text: f"https://example/{target}",
        ideas_lab_enabled=True,
    )
```

Then add:

```python
class IdeaTests(unittest.TestCase):
    def test_idea_no_text_shows_usage(self):
        ctx = make_ctx()
        handlers.cmd_idea(ctx, 1, "")
        self.assertIn("Usage", ctx.bot.sent[-1]["text"])
        self.assertEqual(ctx.idea_pending, {})

    def test_idea_text_shows_target_picker_with_ideas_lab(self):
        ctx = make_ctx()
        handlers.cmd_idea(ctx, 1, "kanban for issues")
        self.assertIn(1, ctx.idea_pending)
        self.assertEqual(ctx.idea_pending[1]["text"], "kanban for issues")
        kb = ctx.bot.sent[-1]["reply_markup"]["inline_keyboard"]
        labels = [btn["text"] for row in kb for btn in row]
        self.assertTrue(any("ideas-lab" in l for l in labels))
        self.assertTrue(any("foo" in l for l in labels))

    def test_idea_target_callback_captures(self):
        ctx = make_ctx()
        handlers.cmd_idea(ctx, 1, "an idea")
        msg_id = ctx.idea_pending[1]["message_id"]
        # tap the ideas-lab button (data "idea:ideas-lab")
        handlers.on_callback(ctx, 1, "cb1", "idea:ideas-lab", msg_id)
        self.assertNotIn(1, ctx.idea_pending)  # cleared
        self.assertIn("captured", ctx.bot.edited[-1]["text"].lower())
        self.assertIn("https://example/ideas-lab", ctx.bot.edited[-1]["text"])

    def test_idea_callback_unconfigured_lab_button_absent(self):
        ctx = make_ctx()
        ctx.ideas_lab_enabled = False
        handlers.cmd_idea(ctx, 1, "x")
        kb = ctx.bot.sent[-1]["reply_markup"]["inline_keyboard"]
        labels = [btn["text"] for row in kb for btn in row]
        self.assertFalse(any("ideas-lab" in l for l in labels))
```

- [ ] **Step 2: Run to verify failure**

Run: `cd bridge-bot && python3 -m unittest tests.test_handlers -v`
Expected: FAIL — `Context.__init__` missing `idea_pending`/`capture_idea`/`ideas_lab_enabled`, and `cmd_idea` undefined.

- [ ] **Step 3: Implement handlers**

In `bridge-bot/handlers.py`, add the three fields to the `Context` dataclass (after `status_provider`):

```python
    idea_pending: dict  # chat_id -> {"text": str, "message_id": int, "targets": list[str]}
    capture_idea: Callable[[str, str], str]  # (target, text) -> link; raises on failure
    ideas_lab_enabled: bool  # whether the "ideas-lab" target is offered
```

Add to `HELP_TEXT` a line: `"  /idea <text>    Capture an idea (then pick a target)\n"`.

Add the handler and callback helpers:

```python
IDEA_LAB_TARGET = "ideas-lab"


def cmd_idea(ctx: Context, chat_id: int, args: str) -> None:
    text = args.strip()
    if not text:
        ctx.bot.send_message(chat_id, "Usage: /idea <your idea text>")
        return
    targets: list[str] = []
    if ctx.ideas_lab_enabled:
        targets.append(IDEA_LAB_TARGET)
    targets.extend(ctx.mru_provider())
    rows = []
    for i, tgt in enumerate(targets):
        label = "📋 ideas-lab (no project)" if tgt == IDEA_LAB_TARGET else _basename(tgt)
        rows.append([{"text": label, "callback_data": f"idea:{tgt}"}])
    rows.append([{"text": "✖ cancel", "callback_data": "idea_cancel"}])
    sent = ctx.bot.send_message(
        chat_id, f"Capture idea — pick a target:\n<i>{text}</i>",
        reply_markup={"inline_keyboard": rows}, parse_mode="HTML",
    )
    ctx.idea_pending[chat_id] = {"text": text, "message_id": sent["message_id"], "targets": targets}
```

In `on_callback`, add — **before** the `state = ctx.pickers.get(...)` line (so it's handled like `kill_confirm:`) — these branches:

```python
    if data == "idea_cancel":
        ctx.idea_pending.pop(chat_id, None)
        ctx.bot.answer_callback_query(callback_id, "Cancelled")
        ctx.bot.edit_message_text(chat_id, message_id, "Cancelled.", reply_markup={"inline_keyboard": []})
        return
    if data.startswith("idea:"):
        pending = ctx.idea_pending.pop(chat_id, None)
        if not pending:
            ctx.bot.answer_callback_query(callback_id, "Idea expired — /idea to restart")
            return
        target = data.split(":", 1)[1]
        ctx.bot.answer_callback_query(callback_id, "Capturing…")
        try:
            link = ctx.capture_idea(target, pending["text"])
            msg = f"✅ captured → {link}"
        except Exception as e:  # surfaced to the user, not swallowed
            msg = f"❌ capture failed: {e}"
        ctx.bot.edit_message_text(chat_id, message_id, msg, reply_markup={"inline_keyboard": []})
        return
```

- [ ] **Step 4: Wire dispatch + the real callable in `bridge_bot.py`**

In `bridge-bot/bridge_bot.py`, add a dispatch branch next to the other commands (near the `/new`/`/status`/`/kill` dispatch):

```python
    elif cmd == "idea":
        handlers.cmd_idea(ctx, chat_id, rest.strip())
```

Where the `Context` is constructed, add the three new fields. The `capture_idea` callable shells out to `bridge capture idea`, piping text via stdin (mirror the existing `bridge` shell-out — reuse the resolved `BRIDGE_BIN` from the iproute2 fix):

```python
    def _capture_idea(target: str, text: str) -> str:
        proc = subprocess.run(
            [BRIDGE_BIN, "capture", "idea", "--target", target],
            input=text, capture_output=True, text=True, timeout=30,
        )
        if proc.returncode != 0:
            raise RuntimeError((proc.stderr or proc.stdout).strip() or "capture failed")
        return proc.stdout.strip()
```

Set `idea_pending={}`, `capture_idea=_capture_idea`, and `ideas_lab_enabled=bool(os.environ.get("BRIDGE_IDEAS_LAB_REPO"))` in the `Context(...)` construction. (Confirm `BRIDGE_BIN` and `subprocess`/`os` are already in scope in `bridge_bot.py` — they are, from the spawn/status paths.)

- [ ] **Step 5: Run tests to verify pass**

Run: `cd bridge-bot && python3 -m unittest discover tests -v`
Expected: PASS (the new IdeaTests + all existing bot tests).

- [ ] **Step 6: Commit**

```bash
git add bridge-bot/handlers.py bridge-bot/bridge_bot.py bridge-bot/tests/test_handlers.py
git commit -m "feat(bridge-bot): /idea capture (text-first, pick target, shell out)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Full verification + smoke

**Files:** none.

- [ ] **Step 1: Go gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # empty
go vet ./...                          # clean
go test -race ./...                   # all ok, incl. forge/capture/remote/cmd
```

- [ ] **Step 2: bridge-bot tests**

Run: `cd bridge-bot && python3 -m unittest discover tests -v`
Expected: all pass.

- [ ] **Step 3: Live smoke (best-effort, real capture)**

Run (writes a real idea to a repo's ideas.md — use a throwaway/test repo or your own bridge repo):
```bash
just build
echo "smoke test idea from bridge capture" | BRIDGE_GITHUB_API= GH_TOKEN="$(direnv exec ~/projects/repos/github/freaxnx01/private sh -c 'printf %s "$GH_TOKEN"')" bridge capture idea --target bridge
```
Expected: prints the `ideas.md` html_url; visiting it shows the appended bullet. (If the token lacks `repo` scope, you get a 403/404 error — that's the documented prerequisite.) Then verify `ideas-lab`:
```bash
echo "smoke ideas-lab capture" | BRIDGE_IDEAS_LAB_REPO=freaxnx01/ideas-lab GH_TOKEN="…(project/repo-scoped token)…" bridge capture idea --target ideas-lab
```
Report the URLs (or the errors).

- [ ] **Step 4: Report**

Report Steps 1-3 output. No success claims without output.

---

## Notes for the implementer

- **No local git** — capture writes via the Contents API only. `internal/capture` must stay forge-token-free (takes a `FileWriter`).
- **Token needs `repo` scope** for Contents-API writes; `remote.GitHubToken` resolves the per-owner direnv token. The error message must name the owner + the scope hint.
- **Don't build issue/roadmap capture** — later #2 slices. Only `capture idea` now.
- **Reuse, don't duplicate:** grep for an existing clock/`timeNow` helper in `cmd/bridge` before adding one; reuse `discoverAllRoots`/`reposRoots`/`BRIDGE_BIN`.
- **Stdin for idea text** — never pass free text via argv (escaping/leading-dash hazards the create path already learned).
- **`on_callback` ordering** — the `idea:`/`idea_cancel` branches go before the spawn-picker `state` lookup, like `kill_confirm:`, so they don't trip the "picker expired" path.
- If you hit a blocker, find the fix and note it inline here.
```
