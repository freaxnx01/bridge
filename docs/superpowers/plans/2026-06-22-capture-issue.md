# Issue capture (#2b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture an issue from mobile — `/issue <title>` in bridge-bot → tap a repo → an issue is created on its forge (GitHub or Forgejo) via the existing `bridge` CLI, with a confirmation link.

**Architecture:** Mirrors the #2a idea-capture pattern. Adds `CreateIssue` to both `GithubClient` and `ForgejoClient` (concrete, not on the `Client` interface — matches `CreateRepo`); an `IssueCreator` consumer interface + `CaptureIssue` in `internal/capture`; a `bridge capture issue` CLI subcommand with title via stdin; a bridge-bot `/issue` flow (text-first → MRU picker, no ideas-lab pin → shell out). Title-only by design (no body).

**Tech Stack:** Go (stdlib `net/http`/`encoding/json`/`testing`/`httptest`), Cobra, Python 3 (bridge-bot, stdlib `unittest`). Spec: `docs/superpowers/specs/2026-06-22-capture-issue-design.md`.

**Resolved open question:** instead of overloading `resolveCaptureTarget`, add a small sibling **`resolveIssueTarget(target string, repos []core.Repo) (issueTarget, error)`** that returns `{Owner, Repo, Forge}` — the forge belongs to the issue path's return shape (used to pick which client to build) and isn't relevant to idea capture. Cleaner than a bool flag.

---

## File Structure

- **Modify** `internal/forge/github.go` — `(*GithubClient).CreateIssue`.
- **Modify** `internal/forge/forgejo.go` — `(*ForgejoClient).CreateIssue`.
- **Modify** `internal/forge/github_test.go` — stub test.
- **Modify** `internal/forge/forgejo_test.go` — stub test.
- **Modify** `internal/capture/capture.go` — `IssueCreator`, `CaptureIssue`.
- **Modify** `internal/capture/capture_test.go` — fake-IssueCreator tests.
- **Modify** `internal/remote/remote.go` — `ForgejoToken`.
- **Modify** `internal/remote/remote_test.go` — token-helper test.
- **Modify** `cmd/bridge/capture.go` — `capture issue` subcommand + `resolveIssueTarget` + `runCaptureIssue`.
- **Modify** `cmd/bridge/capture_test.go` — `TestResolveIssueTarget`.
- **Modify** `bridge-bot/handlers.py` — `Context` fields, `cmd_issue`, `on_callback` issue branches, help.
- **Modify** `bridge-bot/bridge_bot.py` — dispatch `/issue` + `_capture_issue` shell-out.
- **Modify** `bridge-bot/tests/test_handlers.py` — `IssueTests`.

---

## Task 1: forge `CreateIssue` (GitHub + Forgejo)

**Files:**
- Modify: `internal/forge/github.go`
- Modify: `internal/forge/forgejo.go`
- Test: `internal/forge/github_test.go`, `internal/forge/forgejo_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/forge/github_test.go`:

```go
func TestGithubCreateIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/freaxnx01/bridge/issues" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":142,"title":"flicker","html_url":"https://github.com/freaxnx01/bridge/issues/142"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	is, err := c.CreateIssue(context.Background(), "freaxnx01", "bridge", "flicker", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["title"] != "flicker" || gotBody["body"] != "" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if is.Forge != "github" || is.Repo != "freaxnx01/bridge" || is.Number != 142 || is.Title != "flicker" ||
		is.URL != "https://github.com/freaxnx01/bridge/issues/142" {
		t.Errorf("issue: %+v", is)
	}
}
```

Append to `internal/forge/forgejo_test.go`:

```go
func TestForgejoCreateIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/freax/notes/issues" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7,"title":"rough idea","html_url":"https://fj.example/freax/notes/issues/7"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	is, err := c.CreateIssue(context.Background(), "freax", "notes", "rough idea", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["title"] != "rough idea" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if is.Forge != "forgejo" || is.Repo != "freax/notes" || is.Number != 7 || is.URL != "https://fj.example/freax/notes/issues/7" {
		t.Errorf("issue: %+v", is)
	}
}
```

Add `"encoding/json"` to `forgejo_test.go` imports if not already present (`github_test.go` already imports it).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/forge/ -run 'TestGithubCreateIssue|TestForgejoCreateIssue' -v`
Expected: FAIL — `CreateIssue` undefined on both clients.

- [ ] **Step 3: Implement `GithubClient.CreateIssue`**

Add to `internal/forge/github.go` (near `CreateRepo`):

```go
// CreateIssue creates an issue on owner/repo and returns the minimal Issue.
func (c *GithubClient) CreateIssue(ctx context.Context, owner, repo, title, body string) (Issue, error) {
	req := map[string]any{"title": title, "body": body}
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.post(ctx, "/repos/"+owner+"/"+repo+"/issues", req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge:  "github",
		Repo:   owner + "/" + repo,
		Number: raw.Number,
		Title:  raw.Title,
		URL:    raw.HTMLURL,
	}, nil
}
```

- [ ] **Step 4: Implement `ForgejoClient.CreateIssue`**

Add to `internal/forge/forgejo.go` (near `CreateRepo`):

```go
// CreateIssue creates an issue on owner/repo via Forgejo/Gitea and returns the
// minimal Issue.
func (c *ForgejoClient) CreateIssue(ctx context.Context, owner, repo, title, body string) (Issue, error) {
	req := map[string]any{"title": title, "body": body}
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.post(ctx, "/api/v1/repos/"+owner+"/"+repo+"/issues", req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge:  "forgejo",
		Repo:   owner + "/" + repo,
		Number: raw.Number,
		Title:  raw.Title,
		URL:    raw.HTMLURL,
	}, nil
}
```

- [ ] **Step 5: Run + format + commit**

Run: `go test ./internal/forge/ -run 'TestGithubCreateIssue|TestForgejoCreateIssue' -v && go test ./internal/forge/`
Expected: both new tests PASS; full forge package green.
Run: `gofmt -l internal/forge/ && go vet ./internal/forge/`
Expected: clean.

```bash
git add internal/forge/github.go internal/forge/forgejo.go internal/forge/github_test.go internal/forge/forgejo_test.go
git commit -m "feat(forge): CreateIssue on Github + Forgejo clients

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `internal/capture` — `IssueCreator` + `CaptureIssue`

**Files:**
- Modify: `internal/capture/capture.go`
- Test: `internal/capture/capture_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/capture/capture_test.go`:

```go
type fakeIssueCreator struct {
	gotOwner, gotRepo, gotTitle, gotBody string
	ret                                  forge.Issue
	err                                  error
}

func (f *fakeIssueCreator) CreateIssue(_ context.Context, owner, repo, title, body string) (forge.Issue, error) {
	f.gotOwner, f.gotRepo, f.gotTitle, f.gotBody = owner, repo, title, body
	return f.ret, f.err
}

func TestCaptureIssue_TrimsAndCreates(t *testing.T) {
	w := &fakeIssueCreator{
		ret: forge.Issue{Forge: "github", Repo: "freaxnx01/bridge", Number: 142,
			Title: "flicker", URL: "https://github.com/freaxnx01/bridge/issues/142"},
	}
	got, err := CaptureIssue(context.Background(), w, "freaxnx01", "bridge", "  flicker  ")
	if err != nil {
		t.Fatal(err)
	}
	if w.gotTitle != "flicker" {
		t.Errorf("title not trimmed: %q", w.gotTitle)
	}
	if w.gotBody != "" {
		t.Errorf("body must be empty (title-only capture), got %q", w.gotBody)
	}
	if got.Number != 142 || got.URL == "" {
		t.Errorf("returned issue: %+v", got)
	}
}

func TestCaptureIssue_EmptyTitleRejected(t *testing.T) {
	w := &fakeIssueCreator{}
	if _, err := CaptureIssue(context.Background(), w, "freaxnx01", "bridge", "   "); err == nil {
		t.Errorf("empty title must error")
	}
}
```

The `fakeIssueCreator` lives in the test file (one per package). `forge` is already imported in `capture_test.go`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/capture/ -run TestCaptureIssue -v`
Expected: FAIL — `CaptureIssue`/`IssueCreator` undefined.

- [ ] **Step 3: Implement**

Append to `internal/capture/capture.go`:

```go
// IssueCreator is the consumer interface for CaptureIssue. Both
// *forge.GithubClient and *forge.ForgejoClient satisfy it.
type IssueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// CaptureIssue creates a title-only issue on the chosen repo's forge and
// returns the created Issue. The body is always empty by design (title-only
// capture); a future ergonomics step can add an optional body.
func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title string) (forge.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return forge.Issue{}, fmt.Errorf("empty issue title")
	}
	return w.CreateIssue(ctx, owner, repo, title, "")
}
```

(`forge` is already imported by `capture.go`; `fmt`, `strings`, `context` are all already in the package.)

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/capture/ -v`
Expected: all tests pass (existing + new two).
Run: `gofmt -l internal/capture/ && go vet ./internal/capture/`
Expected: clean.

```bash
git add internal/capture/capture.go internal/capture/capture_test.go
git commit -m "feat(capture): CaptureIssue (title-only, forge-agnostic via IssueCreator)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `internal/remote.ForgejoToken`

**Files:**
- Modify: `internal/remote/remote.go`
- Test: `internal/remote/remote_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/remote/remote_test.go`:

```go
func TestForgejoToken_ResolvesFromGitForgejoDir(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "git-forgejo"))
	t.Setenv("FORGEJO_TOKEN", "fj-tok")

	tok, ok := ForgejoToken([]string{root})
	if !ok || tok != "fj-tok" {
		t.Errorf("ForgejoToken = %q,%v, want fj-tok,true", tok, ok)
	}
}

func TestForgejoToken_NoneFound(t *testing.T) {
	root := t.TempDir() // no git-forgejo dir
	t.Setenv("FORGEJO_TOKEN", "")
	if _, ok := ForgejoToken([]string{root}); ok {
		t.Errorf("missing git-forgejo dir should not resolve")
	}
}
```

(`mustMkdirEnvrc` and `filepath` are already in `remote_test.go`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/remote/ -run TestForgejoToken -v`
Expected: FAIL — `ForgejoToken` undefined.

- [ ] **Step 3: Implement**

Append to `internal/remote/remote.go` (near `GitHubToken`):

```go
// ForgejoToken resolves the Forgejo token from the (owner-less) git-forgejo
// .envrc scope across roots, mirroring how fetchTargetRepos resolves it
// internally. Returns ok=false when no git-forgejo dir is found or the token
// is empty.
func ForgejoToken(roots []string) (string, bool) {
	for _, root := range roots {
		for _, t := range discoverRemoteTargets(root) {
			if t.Forge != "forgejo" {
				continue
			}
			tok := envFromDirenv(t.Dir, []string{"FORGEJO_TOKEN"})["FORGEJO_TOKEN"]
			if tok != "" {
				return tok, true
			}
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/remote/ -run TestForgejoToken -v && go test ./internal/remote/`
Expected: new tests PASS; full package green.

```bash
git add internal/remote/remote.go internal/remote/remote_test.go
git commit -m "feat(remote): ForgejoToken resolver (mirrors fetchTargetRepos)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `bridge capture issue` CLI

**Files:**
- Modify: `cmd/bridge/capture.go`
- Test: `cmd/bridge/capture_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cmd/bridge/capture_test.go`:

```go
func TestResolveIssueTarget(t *testing.T) {
	repos := []core.Repo{
		{Owner: "freaxnx01", Name: "bridge", Forge: "github"},
		{Owner: "freaxnx01", Name: "agent-os", Forge: "github"},
		{Owner: "freax", Name: "notes", Forge: "forgejo"},
	}
	// bare name, github
	got, err := resolveIssueTarget("bridge", repos)
	if err != nil || got.Owner != "freaxnx01" || got.Repo != "bridge" || got.Forge != "github" {
		t.Fatalf("bridge: %+v err=%v", got, err)
	}
	// bare name, forgejo
	got, err = resolveIssueTarget("notes", repos)
	if err != nil || got.Forge != "forgejo" || got.Owner != "freax" {
		t.Fatalf("notes: %+v err=%v", got, err)
	}
	// explicit owner/name with forge derived from match
	got, err = resolveIssueTarget("freaxnx01/agent-os", repos)
	if err != nil || got.Forge != "github" || got.Repo != "agent-os" {
		t.Fatalf("owner/name: %+v err=%v", got, err)
	}
	// explicit owner/name with no match in discovered repos -> error (we need the forge)
	if _, err := resolveIssueTarget("someone/unknown", repos); err == nil {
		t.Errorf("unknown owner/name should error (forge unknown)")
	}
	// ideas-lab not valid for issues
	if _, err := resolveIssueTarget("ideas-lab", repos); err == nil {
		t.Errorf("ideas-lab target is for ideas only")
	}
	// unknown bare name
	if _, err := resolveIssueTarget("nope", repos); err == nil {
		t.Errorf("unknown repo should error")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/bridge/ -run TestResolveIssueTarget -v`
Expected: FAIL — `resolveIssueTarget` undefined.

- [ ] **Step 3: Implement the resolver + subcommand**

Add to `cmd/bridge/capture.go` (you'll add `forge` to the imports — it's not currently imported here; also confirm `remote` already is):

```go
// issueTarget is what resolveIssueTarget returns: identifies the repo AND its
// forge (which we need to pick the right client + token).
type issueTarget struct {
	Owner, Repo, Forge string
}

// resolveIssueTarget maps a --target string ("owner/name" or a bare repo name)
// to an issueTarget by looking it up in the discovered repos. Unlike the
// idea-capture resolver, this one accepts both github and forgejo repos and
// rejects the "ideas-lab" sentinel.
func resolveIssueTarget(target string, repos []core.Repo) (issueTarget, error) {
	if target == "ideas-lab" {
		return issueTarget{}, fmt.Errorf("ideas-lab target is for ideas only; pick a repo")
	}
	if owner, name, ok := strings.Cut(target, "/"); ok {
		for i := range repos {
			if strings.EqualFold(repos[i].Owner, owner) && strings.EqualFold(repos[i].Name, name) {
				return issueTarget{Owner: repos[i].Owner, Repo: repos[i].Name, Forge: repos[i].Forge}, nil
			}
		}
		return issueTarget{}, fmt.Errorf("no known repo %s/%s (need its forge to create the issue)", owner, name)
	}
	var match *core.Repo
	for i := range repos {
		f := repos[i].Forge
		if (f == "github" || f == "forgejo") && strings.EqualFold(repos[i].Name, target) {
			if match != nil {
				return issueTarget{}, fmt.Errorf("repo %q is ambiguous; use owner/name", target)
			}
			match = &repos[i]
		}
	}
	if match == nil {
		return issueTarget{}, fmt.Errorf("no github/forgejo repo named %q", target)
	}
	return issueTarget{Owner: match.Owner, Repo: match.Name, Forge: match.Forge}, nil
}

var captureIssueCmd = &cobra.Command{
	Use:   "issue",
	Short: "Capture an issue (title from stdin) on a chosen repo",
	RunE:  runCaptureIssue,
}

var captureIssueTarget string

func init() {
	captureIssueCmd.Flags().StringVar(&captureIssueTarget, "target", "", "<repo-name> | <owner>/<name>")
	_ = captureIssueCmd.MarkFlagRequired("target")
	captureCmd.AddCommand(captureIssueCmd)
}

func runCaptureIssue(cmd *cobra.Command, args []string) error {
	repos, _ := discoverAllRoots()
	tgt, err := resolveIssueTarget(captureIssueTarget, repos)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read title: %w", err)
	}
	title := firstNonEmptyLine(string(raw))
	if title == "" {
		return fmt.Errorf("no title on stdin")
	}
	var creator capture.IssueCreator
	switch tgt.Forge {
	case "github":
		tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
		if !ok {
			return fmt.Errorf("no github token for owner %q (need an .envrc GH_TOKEN with repo scope)", tgt.Owner)
		}
		creator = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
	case "forgejo":
		tok, ok := remote.ForgejoToken(reposRoots())
		if !ok {
			return fmt.Errorf("no forgejo token (need a git-forgejo .envrc with FORGEJO_TOKEN)")
		}
		creator = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
	default:
		return fmt.Errorf("forge %q is not supported for issue capture", tgt.Forge)
	}
	is, err := capture.CaptureIssue(cmd.Context(), creator, tgt.Owner, tgt.Repo, title)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), is.URL)
	return nil
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
```

Add the necessary imports to `cmd/bridge/capture.go`:
- `"github.com/freaxnx01/bridge/internal/forge"` — needed (this file didn't import forge before; idea capture went through `internal/capture` directly, but `creator` is typed `capture.IssueCreator` and we still need `forge.NewGithubClient`/`NewForgejoClient`).
- (already imported: `context` via cmd.Context, `fmt`, `io`, `os`, `strings`, `cobra`, `core`, `capture`, `remote`.)

- [ ] **Step 4: Run + commit**

Run:
```
go test ./cmd/bridge/ -run TestResolveIssueTarget -v
go test ./cmd/bridge/
go build ./...
gofmt -l cmd/bridge/ | grep -v worktrees
go vet ./cmd/bridge/
```
Expected: tests pass; build OK; gofmt empty; vet clean.

```bash
git add cmd/bridge/capture.go cmd/bridge/capture_test.go
git commit -m "feat(bridge): capture issue CLI (title stdin, github+forgejo target)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: bridge-bot `/issue`

**Files:**
- Modify: `bridge-bot/handlers.py`
- Modify: `bridge-bot/bridge_bot.py`
- Test: `bridge-bot/tests/test_handlers.py`

- [ ] **Step 1: Write the failing tests**

Append to `bridge-bot/tests/test_handlers.py`. First, extend `make_ctx` to inject the new fields — find the existing `make_ctx` (already extended for idea capture) and add to its `handlers.Context(...)` call:

```python
        issue_pending={},
        capture_issue=lambda target, title: f"https://example/{target}/issues/1",
```

Then add the tests:

```python
class IssueTests(unittest.TestCase):
    def test_issue_no_title_shows_usage(self):
        ctx = make_ctx()
        handlers.cmd_issue(ctx, 1, "")
        self.assertIn("Usage", ctx.bot.sent[-1]["text"])
        self.assertEqual(ctx.issue_pending, {})

    def test_issue_title_shows_target_picker_without_ideas_lab(self):
        ctx = make_ctx()
        handlers.cmd_issue(ctx, 1, "flicker on rapid typing")
        self.assertIn(1, ctx.issue_pending)
        self.assertEqual(ctx.issue_pending[1]["title"], "flicker on rapid typing")
        kb = ctx.bot.sent[-1]["reply_markup"]["inline_keyboard"]
        labels = [btn["text"] for row in kb for btn in row]
        self.assertTrue(any("foo" in l for l in labels))  # MRU repo present
        self.assertFalse(any("ideas-lab" in l for l in labels))  # NO ideas-lab pin

    def test_issue_target_callback_creates(self):
        ctx = make_ctx()
        handlers.cmd_issue(ctx, 1, "a title")
        msg_id = ctx.bot.sent[-1]["message_id"]
        handlers.on_callback(ctx, 1, "cb1", "issue:foo", msg_id)
        self.assertNotIn(1, ctx.issue_pending)  # cleared
        self.assertIn("created", ctx.bot.edited[-1]["text"].lower())
        self.assertIn("https://example/foo/issues/1", ctx.bot.edited[-1]["text"])

    def test_issue_callback_capture_failure_shown(self):
        ctx = make_ctx()
        def _fail(target, title): raise RuntimeError("scope missing")
        ctx.capture_issue = _fail
        handlers.cmd_issue(ctx, 1, "a title")
        msg_id = ctx.bot.sent[-1]["message_id"]
        handlers.on_callback(ctx, 1, "cb1", "issue:foo", msg_id)
        self.assertIn("failed", ctx.bot.edited[-1]["text"].lower())
        self.assertIn("scope missing", ctx.bot.edited[-1]["text"])
```

- [ ] **Step 2: Run to verify failure**

Run: `cd bridge-bot && python3 -m unittest tests.test_handlers -v`
Expected: FAIL — `Context.__init__()` complains about missing `issue_pending`/`capture_issue`, and `cmd_issue` undefined.

- [ ] **Step 3: Implement handlers**

In `bridge-bot/handlers.py`, add the two fields to the `Context` dataclass (after `ideas_lab_enabled`/`repo_creator`/`pending`):

```python
    issue_pending: dict  # chat_id -> {"title": str}
    capture_issue: Callable[[str, str], str]  # (target, title) -> link; raises on failure
```

Add to `HELP_TEXT`: `"  /issue <title>  Capture an issue (then pick a target repo)\n"`.

Add the handler (after `cmd_idea`):

```python
def cmd_issue(ctx: Context, chat_id: int, args: str) -> None:
    title = args.strip()
    if not title:
        ctx.bot.send_message(chat_id, "Usage: /issue <title>")
        return
    targets = list(ctx.mru_provider())  # no ideas-lab pin — issues need a real repo
    rows = []
    for tgt in targets:
        rows.append([{"text": _basename(tgt), "callback_data": f"issue:{tgt}"}])
    rows.append([{"text": "✖ cancel", "callback_data": "issue_cancel"}])
    ctx.bot.send_message(
        chat_id, f"Capture issue — pick a repo:\n<i>{html.escape(title)}</i>",
        reply_markup={"inline_keyboard": rows}, parse_mode="HTML",
    )
    ctx.issue_pending[chat_id] = {"title": title}
```

In `on_callback`, add — **before** the `state = ctx.pickers.get(...)` lookup (mirrors the `idea:`/`idea_cancel` placement) — these branches:

```python
    if data == "issue_cancel":
        ctx.issue_pending.pop(chat_id, None)
        ctx.bot.answer_callback_query(callback_id, "Cancelled")
        ctx.bot.edit_message_text(chat_id, message_id, "Cancelled.", reply_markup={"inline_keyboard": []})
        return
    if data.startswith("issue:"):
        pending = ctx.issue_pending.pop(chat_id, None)
        if not pending:
            ctx.bot.answer_callback_query(callback_id, "Issue expired — /issue to restart")
            return
        target = data.split(":", 1)[1]
        ctx.bot.answer_callback_query(callback_id, "Creating…")
        try:
            link = ctx.capture_issue(target, pending["title"])
            msg = f"✅ created → {link}"
        except Exception as e:  # surfaced to the user, not swallowed
            msg = f"❌ create failed: {e}"
        ctx.bot.edit_message_text(chat_id, message_id, msg, reply_markup={"inline_keyboard": []})
        return
```

(`html` is already imported by `handlers.py` for the `/idea` HTML-escape fix.)

- [ ] **Step 4: Wire dispatch + the real callable in `bridge_bot.py`**

Add a dispatch branch in `bridge_bot.py` near the other commands:

```python
    elif cmd == "issue":
        handlers.cmd_issue(ctx, chat_id, rest.strip())
```

Add `_capture_issue` (mirroring `_capture_idea`'s `BRIDGE_BIN` argv shape):

```python
def _capture_issue(target: str, title: str) -> str:
    """Shell out to `bridge capture issue --target <target>`, piping title via stdin."""
    proc = subprocess.run(
        [BRIDGE_BIN, "capture", "issue", "--target", target],
        input=title, capture_output=True, text=True, timeout=30,
        env=spawn.clean_env(),
    )
    if proc.returncode != 0:
        raise RuntimeError((proc.stderr or proc.stdout).strip() or "create failed")
    return proc.stdout.strip()
```

Add `issue_pending={}` and `capture_issue=_capture_issue` to the `Context(...)` construction in `build_context`.

- [ ] **Step 5: Run tests to verify pass**

Run: `cd bridge-bot && python3 -m unittest discover tests -v`
Expected: all bot tests pass (existing + the 4 new `IssueTests`).

- [ ] **Step 6: Commit**

```bash
git add bridge-bot/handlers.py bridge-bot/bridge_bot.py bridge-bot/tests/test_handlers.py
git commit -m "feat(bridge-bot): /issue capture (text-first, pick repo, shell out)

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
go test -race ./...                   # all ok
```

- [ ] **Step 2: bridge-bot tests**

Run: `cd bridge-bot && python3 -m unittest discover tests -v`
Expected: all pass (the 78-from-prior-cycle + the 4 new `IssueTests`).

- [ ] **Step 3: Live smoke (best-effort, creates a REAL issue)**

Run (creates a real issue on `bridge` — use a throwaway issue you can close):
```bash
just build
echo "smoke: /issue capture from bridge CLI" | bridge capture issue --target bridge
```
Expected: prints the issue URL. Visit it to confirm.

For Forgejo, repeat with a forgejo repo target:
```bash
echo "smoke: forgejo issue" | bridge capture issue --target <a-forgejo-repo-name>
```

- [ ] **Step 4: Report**

Report Steps 1-3 output. No success claims without output.

---

## Notes for the implementer

- **No local git, no Contents API** — issues go straight through forge POST. `internal/capture` stays forge-token-free (takes `IssueCreator`).
- **GitHub token needs `repo` scope** for issue create on private repos (`public_repo` for public-only). Forgejo token needs the `write:issue` scope on the user account. Errors surface verbatim.
- **Title from stdin**, never argv. Multi-line input → first non-empty line; the rest is ignored (we explicitly chose title-only). Bodies are a future ergonomics step (out of scope).
- **`on_callback` ordering** — `issue:`/`issue_cancel` branches go before the spawn-picker `state` lookup, like `idea:`/`idea_cancel`, so they don't trip the "picker expired" path.
- **Reuse, don't duplicate** — the bot's MRU provider is already wired and used by `/idea`/`/newrepo`; reuse it; do NOT introduce a new repo provider.
- If you hit a blocker, find the fix and note it inline here for the next run.
