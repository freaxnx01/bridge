# #2b: Issue capture (mobile → GitHub/Forgejo Issue)

**Date:** 2026-06-22
**Area:** `internal/forge` (`CreateIssue` on Github + Forgejo) · `internal/capture` · `cmd/bridge` (`capture issue` subcommand) · `bridge-bot` (`/issue`)
**Status:** Approved (design)

## Context

Second slice of **decomposition #2 (capture/routing)**. Idea capture (#2a) shipped
2026-06-21: `/idea <text>` → tap target → commit via the GitHub Contents API.
This slice adds **issue capture**: `/issue <title>` → tap repo → create a real
issue on its forge (GitHub or Forgejo). The codebase already has the scaffolding
(`internal/capture` pattern, `capture` CLI group, bot target-picker), so this
slice is mostly a mirror of #2a with a different write path.

The third slice (**roadmap capture**, adding to a GitHub Projects v2 board)
remains a later piece — deferred.

## Goal (success criterion)

From the phone: `/issue <title>` → tap a repo from the MRU picker → an issue is
created on that repo's forge (GitHub or Forgejo) → the bot replies with the new
issue's URL and number. The same path works on the CLI: `bridge capture issue
--target <repo>` with the title piped via stdin.

## Decisions (from brainstorming)

1. **Title-only.** The captured text becomes the issue title; the body is empty.
   Matches "capture at lowest fidelity" — fastest on mobile; you flesh out the
   body in GitHub later if needed. Issue *bodies* are deferred (non-goal).
2. **Forge scope = GitHub + Forgejo.** Both clients gain `CreateIssue`. Issue
   creation is uniform across forges (POST title+body, returns the issue). The
   target resolver already returns the chosen repo's forge — no UX changes vs.
   `/idea`. GitLab + ADO deferred (their clients have no `CreateIssue` yet;
   YAGNI per the user's actual usage).
3. **Reuse the #2a scaffolding wholesale.** Same `capture` CLI group, same
   target resolver (minus the `ideas-lab` branch — issues need a repo), same
   bot target-picker (minus the ideas-lab pin), same per-owner token resolution.
4. **`CreateIssue` lives on the concrete clients** (mirrors `CreateRepo`), not
   on `forge.Client`. The capture core takes a small `IssueCreator` consumer
   interface — keeps `internal/capture` forge-token-free.

## Architecture

```
  Telegram  /issue <title>        bridge-bot (Python)
     │ tap target repo  ───►  cmd_issue → picker → on_callback
     │                              │ shell out (title via stdin)
     ▼                              ▼
                     bridge capture issue --target <owner/name|repo-name>
                                    │
                                    ▼   internal/capture.CaptureIssue
                                    ▼   IssueCreator (forge.GithubClient | forge.ForgejoClient)
                            POST /repos/{o}/{r}/issues              (GitHub)
                            POST /api/v1/repos/{o}/{r}/issues       (Forgejo)
                                    │
                                    ▼   returns forge.Issue{Forge, Repo, Number, Title, URL}
```

### 1. `internal/forge` — `CreateIssue` on both clients

- **`GithubClient.CreateIssue(ctx, owner, repo, title, body string) (Issue, error)`**:
  uses the existing `post` helper: `c.post(ctx, "/repos/"+owner+"/"+repo+"/issues", {title, body}, &raw)`.
  Decodes `{number, title, html_url}` into the existing `Issue` shape (Forge =
  `"github"`, Repo = `owner+"/"+repo`).
- **`ForgejoClient.CreateIssue(ctx, owner, repo, title, body string) (Issue, error)`**:
  same shape, path `/api/v1/repos/"+owner+"/"+repo+"/issues"`, response fields are
  `{number, title, html_url}` on Forgejo too. Returns `Issue` with Forge =
  `"forgejo"`, Repo = `owner+"/"+repo`.
- **Empty title** rejected at the capture layer (below), not at the forge layer
  — the forge methods are thin POST helpers.

### 2. `internal/capture` — `CaptureIssue`

```go
// IssueCreator is the consumer interface for capture.CaptureIssue (the subset
// the capture core needs from a forge client). Both *forge.GithubClient and
// *forge.ForgejoClient satisfy it.
type IssueCreator interface {
    CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// CaptureIssue trims the title, rejects empty, and creates the issue with an
// empty body (title-only capture, by design).
func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title string) (forge.Issue, error)
```

`internal/capture` stays forge-token-free — same DI pattern as `CaptureIdea`.

### 3. `cmd/bridge` — `bridge capture issue`

New subcommand under the existing `capture` group:

```
bridge capture issue --target <owner/name | repo-name>
```

- `--target` reuses **`resolveCaptureTarget`** from `cmd/bridge/capture.go`, but
  with the `ideas-lab` branch as an explicit error ("ideas-lab target is for
  ideas only; pick a repo"). Bare repo name → case-insensitive match across
  discovered repos; `owner/name` → literal.
- **Title from stdin** (`io.ReadAll(cmd.InOrStdin())`) — keeps free text out of
  argv (same security stance as `capture idea`). Multi-line input → only the
  **first non-empty line** is used as the title; the rest is ignored (we
  explicitly chose title-only).
- Resolves the forge from the matched `core.Repo` (the resolver returns
  `Target` with `Owner`/`Repo`; the surrounding code reads `repos.Forge` to know
  which client to build). Per-forge token: `remote.GitHubToken(roots, owner)`
  for `forge=="github"`, **new** `remote.ForgejoToken(roots) (string, bool)` for
  `forge=="forgejo"` (Forgejo has a single owner-less `.envrc` under
  `git-forgejo/`, mirroring how `internal/remote.fetchTargetRepos` already reads
  it).
- Builds the matching client (`forge.NewGithubClient` / `forge.NewForgejoClient`),
  calls `capture.CaptureIssue`, prints the issue's `URL` to stdout.

### 4. bridge-bot `/issue`

- **`handlers.py`**:
  - `Context` gains `issue_pending: dict` and `capture_issue: Callable[[str, str], str]`
    (target, title → link; raises on failure).
  - `cmd_issue(ctx, chat_id, args)`: empty `args` → "Usage: /issue <title>" + no
    stash. Else: stash `{title: args}` keyed by chat_id, send a target picker.
    Target list = **MRU repos only** (no `ideas-lab` pin; issues need a real
    repo). Callback data `issue:<target>`.
  - `on_callback`: handle `issue_cancel` and `issue:<target>` **before** the
    spawn-picker `state` lookup (same placement as the `idea:` branches added in
    #2a). On success → `✅ created → <link>`; on exception → `❌ create failed: <e>`
    (surfaced, not swallowed). HTML-escape the title in the picker preview (mirror
    the #2a fix).
  - `HELP_TEXT` gains a `/issue <title>` line.
- **`bridge_bot.py`**:
  - `_capture_issue(target, title)` shell-out:
    `[BRIDGE_BIN, "capture", "issue", "--target", target]` with `input=title`,
    `env=spawn.clean_env()`, 30s timeout. Non-zero return → `RuntimeError`. Mirrors
    `_capture_idea`.
  - Dispatch `cmd == "issue"` → `handlers.cmd_issue(ctx, chat_id, rest.strip())`.
  - `build_context` adds `issue_pending={}` and `capture_issue=_capture_issue`.

### 5. `internal/remote` — `ForgejoToken`

Add `ForgejoToken(roots []string) (string, bool)`. Single owner-less scope:
walks roots looking for `git-forgejo/.envrc`, reads `FORGEJO_TOKEN` via the
existing `EnvFromDirenv` (already exported). Mirrors how
`internal/remote.fetchTargetRepos` already resolves the Forgejo token internally.

## Error handling

- **Empty title:** bot rejects (usage hint, no shell-out); CLI errors clearly
  via the capture layer.
- **Bad target:** `resolveCaptureTarget` errors ("no github/forgejo repo named
  X" / "ambiguous; use owner/name" / "ideas-lab target is for ideas only").
- **No token / wrong scope:** forge returns 401/403; bubbled up as
  `"create issue failed: …"`; bot replies with the failure text.
- **Repo not found on forge / archived / issues disabled:** forge returns
  404/410/422; surfaced verbatim. (No special handling; user fixes the repo
  config.)

## Testing

- **`internal/forge`**: `TestGithubCreateIssue` + `TestForgejoCreateIssue`
  against `httptest` stubs. Each asserts the POST body (`title`/`body`), the
  returned `Issue` (Forge, Repo, Number, URL, Title), and an error path
  (non-2xx with body in the error).
- **`internal/capture`**: `TestCaptureIssue` with a hand-rolled fake
  `IssueCreator`: trims whitespace, rejects empty title with a clear error,
  passes the trimmed title + an empty body to the creator, returns the issue
  unchanged on success.
- **`cmd/bridge`**: `TestResolveCaptureTarget_IdeasLabRejectedForIssue` (the
  new branch in the resolver — or a sibling `resolveIssueTarget` if cleaner; see
  below); `TestCaptureIssue_RoutesGithubVsForgejo` with a fake `IssueCreator`
  injected via a small seam to confirm the right client/token is chosen by
  forge.
- **`bridge-bot`**: `IssueTests` (mirroring `IdeaTests`): no title → usage;
  title → picker without `ideas-lab` pin; `issue:<target>` callback calls a fake
  `capture_issue` and replies with the link; capture failure shown.
- Gates: `gofmt`/`go vet`/`golangci-lint`/`go test -race ./...`; the existing
  goldens stay untouched (this slice adds no nav/TUI changes).

## Non-goals

- **Issue bodies** (title-only this slice; trivial to add later by piping a body
  separately or via a `--body-from-stdin` shape — explicitly deferred).
- **Labels, assignees, milestones, projects.**
- **Editing/closing/commenting on issues.**
- **GitLab / ADO** issue creation (their clients have no `CreateIssue`).
- **Roadmap capture** (later slice — adds a Projects v2 card via GraphQL
  mutation; reuses `internal/capture` shape but a different write).

## Open questions / follow-ups

- **Target resolver shape:** the cleanest path is a small refactor of
  `resolveCaptureTarget` in `cmd/bridge/capture.go` to take an `acceptIdeasLab
  bool` parameter (idea → true, issue → false), keeping both subcommands on the
  same resolver. Decided in the plan.
- **Title cleanup:** strip leading `/issue ` if the user accidentally includes
  it (Telegram sends just the args after the command name, so this is mostly
  unnecessary, but worth a sanity-check guard in `cmd_issue`).
- **Issue body capture** is the obvious next ergonomics step (e.g. a
  reply-with-body affordance from the bot's "✅ created" message). Out of scope
  here; lands cleanly later by adding a `--body-stdin-after-title` shape to the
  CLI.
