# #2a: Idea capture (mobile → ideas-lab / repo ideas.md)

**Date:** 2026-06-16
**Area:** `internal/forge` (Contents API) · new `internal/capture` · `cmd/bridge` (`capture` group) · `bridge-bot` (`/idea`)
**Status:** Approved (design)

## Context

This is the first slice of **decomposition #2 (capture/routing)** in the cockpit
reshaping — the mobile capture path the user's real workflow leans on most
("ideas strike out of office"). Decomposition #1 (the cross-repo overview, Plans
1a+1b) is done and merged.

"FlowHub" is not a separate system — the mobile capture front-end is the existing
**`bridge-bot`** (Python Telegram bot: `handlers.py` orchestrating an injectable
`Context`, shelling out to the `bridge` CLI; today it does `/new` spawn,
`/status`, `/kill`). This slice teaches it to **capture an idea** and route it to
a Git-backed destination.

#2's other routes (issue, roadmap capture) are **later slices** — same
core+bot pattern, deferred here.

## Goal (success criterion)

From the phone: `/idea <text>` → tap a target (an MRU repo, or "ideas-lab / no
project") → the idea is committed to GitHub and the bot replies with a link.
Works regardless of any local checkout's state; nothing on the host working tree
is touched. A repo-targeted idea appears in the nav overview's Inbox tier (it
writes the same `ideas.md` that tier already reads).

## Decisions (from brainstorming)

1. **Write via the GitHub Contents API**, not local git — the capture core is
   stateless and reliable from a phone (no dirty-tree / branch / rebase hazards).
2. **Two targets:** a chosen **repo** (append a bullet to its `ideas.md`) **or**
   **ideas-lab** (new `ideas/<date>-slug.md` file). User picks; no classifier.
3. **Headless core + thin bot client** — a Go capture core (CLI) does the work;
   bridge-bot shells out to it. Reusable by CC CLI / future WebUI.
4. **Text-first bot flow:** `/idea <text>` then tap the target (fewest-friction
   capture; the thought is recorded before routing).

## Architecture

```
  Telegram  /idea <text>          bridge-bot (Python)
     │  tap target button   ────►  cmd_idea → picker → on_callback
     │                                   │ shell out (text via stdin)
     ▼                                   ▼
                          bridge capture idea --target <ideas-lab|owner/name>
                                         │
                                         ▼   internal/capture.CaptureIdea
                          ┌──────────────┴───────────────┐
              ideas-lab: create ideas/<date>-slug.md   repo: append bullet to ideas.md
                          └──────────────┬───────────────┘
                                         ▼  internal/forge GetFile / PutFile
                                  GitHub Contents API  (repo-scope token)
```

### 1. `internal/forge/github.go` — Contents API

```go
// GetFile fetches a file's decoded content + blob sha. found=false on 404.
func (c *GithubClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)

// PutFile creates or updates a file via the Contents API (base64-encoded).
// Empty sha => create; a blob sha => update. Returns the file's html_url.
func (c *GithubClient) PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (htmlURL string, err error)
```

- `GetFile`: `GET /repos/{owner}/{repo}/contents/{path}`; decode base64 `content`,
  read `sha`; HTTP 404 → `found=false, err=nil`.
- `PutFile`: `PUT /repos/{owner}/{repo}/contents/{path}` with
  `{message, content: base64(content), sha?}`; return `content.html_url`.
  A 409/422 (sha conflict / already exists) surfaces as an error the caller maps.

### 2. `internal/capture` — the core

```go
// Target is where an idea lands: IdeasLab, or a specific Repo (owner/name).
type Target struct { IdeasLab bool; Owner, Repo string }

// FileWriter is the subset of the forge client capture needs (consumer iface).
type FileWriter interface {
    GetFile(ctx context.Context, owner, repo, path string) ([]byte, string, bool, error)
    PutFile(ctx context.Context, owner, repo, path string, content []byte, message, sha string) (string, error)
}

// CaptureIdea writes the idea to the target and returns the file's URL.
func CaptureIdea(ctx context.Context, w FileWriter, t Target, text string, now time.Time) (string, error)
```

- **ideas-lab:** path `ideas/<YYYY-MM-DD>-<slug>.md`; `slug` derived from `text`
  (lowercased, non-alnum→`-`, collapsed, trimmed, ≤50 chars; empty→`idea`).
  Content:
  ```
  Status: seed
  Captured: <YYYY-MM-DD> (Telegram capture)

  <text>
  ```
  `PutFile(owner, repo, path, content, "capture: <slug>", "")` (create). On
  collision (file exists for that date+slug), suffix `-2`, `-3`, … (probe with
  `GetFile`). Owner/repo for ideas-lab come from config (below).
- **repo:** path `ideas.md`; `GetFile`; if `!found` → `"# Ideas\n\n- <text>\n"`;
  else append `"- <text>\n"` (ensure a trailing newline). `PutFile(..., sha)`
  with the fetched sha (or `""` when creating). Commit message `capture: idea`.
- The `now` is injected (deterministic tests). `\n` line endings.

`internal/capture` is forge-token-free: it takes a `FileWriter` (the consumer
interface). cmd/bridge supplies the real `*forge.GithubClient`.

### 3. `cmd/bridge` — `capture` command group

`bridge capture idea --target <ideas-lab|owner/name>` — idea text read from
**stdin** (avoids argv escaping of free text). Resolves the GitHub token for the
target owner via the existing **per-owner direnv** mechanism (exposed from
`internal/remote` as a small helper, e.g. `remote.GitHubToken(roots, owner)`),
builds a `forge.NewGithubClient(token, BRIDGE_GITHUB_API)`, calls
`capture.CaptureIdea`, prints the URL. `ideas-lab` resolves to the repo from
config `BRIDGE_IDEAS_LAB_REPO` (e.g. `freaxnx01/ideas-lab`); its owner drives the
token resolution. `capture` is a group so `issue`/`roadmap` slot in later.

### 4. `bridge-bot` — `/idea`

- `cmd_idea(ctx, chat_id, args)`: `args` is the idea text. If empty →
  usage hint. Else stash `{chat_id: text}` (a `pending_ideas` map on `Context`,
  mirroring `pickers`) and send a target picker: a pinned **"📋 ideas-lab (no
  project)"** button + MRU repos (reuse `repos.py`/`picker.py`), + cancel.
- `on_callback` (idea-target tap): pop the stashed text, call a new injected
  `ctx.capture_idea(target, text) -> link` callable (shells out to
  `bridge capture idea --target … `, piping text via stdin), reply
  `✓ captured → <link>` or the error. Callback data namespaced (e.g. `idea:<target>`)
  so it doesn't collide with the spawn picker's callbacks.
- `/help` gains an `/idea` line. Reuses the existing allowlist auth.

### Config

- **`BRIDGE_IDEAS_LAB_REPO`** = `owner/name` of the ideas-lab repo (e.g.
  `freaxnx01/ideas-lab`) — the API target for ideas-lab captures. (Distinct from
  `BRIDGE_IDEAS_LAB`, the local *path* the overview reads.) Empty → the
  "ideas-lab" target is disabled (only repo targets offered).
- **Token:** the resolved per-owner GitHub token must have **`repo` scope**
  (Contents API write). The private direnv token has it. Documented prerequisite.

## Error handling

- Token lacks `repo` scope / write denied → Contents API 403/404 → surfaced as a
  CLI error → bot replies with the failure (not a silent drop).
- `ideas-lab` target chosen but `BRIDGE_IDEAS_LAB_REPO` unset → CLI errors
  clearly; bot omits the ideas-lab button when unconfigured.
- Empty idea text → bot usage hint, no shell-out.
- Contents-API sha conflict on `ideas.md` (concurrent edit) → surfaced; capture
  is low-frequency/personal so no retry loop (documented).

## Testing

- **`internal/forge`**: `GetFile`/`PutFile` against an `httptest` stub — create
  (empty sha, asserts base64 body + message), update (sha present), and 404 →
  `found=false`.
- **`internal/capture`**: `CaptureIdea` with a hand-rolled fake `FileWriter`:
  ideas-lab path/slug/preamble + injected `now`; repo append-to-existing (fake
  returns content+sha) vs create-when-absent (`# Ideas` heading); slug edge
  cases (punctuation, length, empty→`idea`); collision suffixing.
- **`cmd/bridge`**: `capture idea` arg/stdin/target parsing; `ideas-lab`
  unset-config error.
- **`bridge-bot`**: `test_handlers.py`-style — `cmd_idea` stashes text + sends a
  picker; the target callback calls a fake `capture_idea` and replies with the
  link; empty-text hint; unconfigured ideas-lab omits the button.
- Gates: `gofmt`/`go vet`/`golangci-lint`/`go test -race ./...`; bridge-bot
  `python3 -m unittest discover tests`.

## Non-goals

- **Issue & roadmap capture** — later #2 slices (same core+bot pattern).
- **Local-git writes** — Contents API only.
- **LLM/auto classification** of the target — the user picks.
- **Editing/deleting** captured ideas; **non-GitHub** forges (ideas live on
  GitHub; Forgejo is a mirror).
- **Commit-to-a-branch/PR** for captures — straight to the default branch (these
  are low-stakes incubator notes).

## Open questions / follow-ups

- **Token resolution helper:** confirm the exact shape exposed from
  `internal/remote` (`GitHubToken(roots, owner)`), reusing its existing
  `discoverRemoteTargets`/`envFromDirenv`. If that proves awkward, fall back to a
  `BRIDGE_CAPTURE_TOKEN` env (repo scope) — decided in the plan.
- **MRU source for the picker:** reuse the bot's existing `mru_provider`/repo
  listing; confirm it surfaces the repos you capture against most.
- Issue/roadmap capture slices reuse `internal/capture` + the `capture` CLI group
  + the bot's target-picker scaffolding.
