# Bridge MCP cross-forge tools — status

_2026-07-31. Tracks #220, #219, #221, #223. Worktree: `.worktrees/new-mcp-cmds`._

## Original scope (4 tools)

A multi-session plan to grow bridge's MCP surface: `list_tree` (#220),
`update_repo` (#219), `search_code` (#221), and tree-writes/PR-opening (#223).
Sessions were explicitly sequenced — do not start #221 or #223 until earlier
ones land and their open questions are settled.

## Done — merged to `main`

- **#220 `list_tree`** — PR #224, merged. Lists a directory (or full tree
  recursively) from a repo's default branch on both forges. Surfaces GitHub's
  `truncated` flag; empty repo → empty list, not an error.
- **#219 `update_repo`** — PR #225, merged. Updates description/topics/
  private/archived, draft-first (`confirm: true` gate). `topics` lives on a
  separate endpoint from the rest on both forges, so a call touching both can
  partially fail — that's reported explicitly (`topics_error` alongside a
  populated `result`), never silently. `archived: true` additionally requires
  `--allow-destructive`.
- **#221 `search_code`** — PR #226, merged, **GitHub-only**. Investigated the
  issue's premise (Forgejo's indexer off by default) and found a more
  fundamental blocker: Forgejo has **no code-search REST API at all**, only
  an HTML search page (confirmed via the instance's `swagger.v1.json` and a
  live probe — the grep-based search itself works fine without the indexer,
  it's just HTML-only). Went with the issue's own recommended option:
  GitHub-only + honest capability reporting via `list_git_forges`. Findings
  posted to the issue as a comment.

All three tools follow the same conventions: capability interfaces in
`internal/mcp/tools.go` (`treeLister`, `repoUpdater`/`topicsSetter`,
`searchCoder`), fan-out-with-warnings for multi-owner reads (mirrors
`list_repos`), draft-first `confirm` gate for writes, live-verified against
real repos before opening each PR.

## Dogfooded (real writes, already done)

Used `update_repo` directly to fix the two stale descriptions the #219 issue
called out:
- `freaxnx01/bridge` — no longer describes the unrelated agent-dev slot
  launcher.
- `freaxnx01/agent-action-sandbox` — no longer references the defunct
  `claude-pipeline` repo; now says `agent-workflow`.

## Discovered, not yet fixed

While live-testing `search_code` against the issue's own motivating case
(remaining `claude-pipeline` references), found one more real stale
reference:

- `freaxnx01/quicktask-vikunja:.github/workflows/claude.yml:43` still says
  `freaxnx01/claude-pipeline@main`.

This is a small, independent fix (not part of #220/#219/#221/#223) — worth a
short separate pass, not folded into the tools work.

## Blocked — needs a decision before starting

**#223 (tree writes / PR-opening)** is not started. It grants PR-opening
capability to every client behind bridge simultaneously — including locutus
on Telegram — so the repo-allowlist policy needs to be decided *in the
issue*, before any code exists. Options discussed but not chosen:

1. Explicit allowlist in config — a configured list of `(forge, owner, repo)`
   tuples that tree-writes are permitted on.
2. Owner-scoped, no repo allowlist — same trust boundary as `create_repo`
   today (any repo under the configured default owners).
3. Reuse the existing `AllowDestructive`-style server flag as the gate
   instead of a per-repo list.

Last time this was asked, the answer was "not ready to decide yet" — so this
is a **stop and ask** point, not a default to implement against.

## Resuming this work

1. If picking #223 back up: ask the user which repo-allowlist policy to use
   (see options above) before writing any code. Once decided, record it on
   the issue itself, then implement following the same conventions used for
   #220/#219/#221 (capability interface in `tools.go`, draft-first `confirm`
   gate since it's a write, live-verify on a designated sandbox repo before
   opening the PR — do not use `freaxnx01/bridge` or
   `freaxnx01/agent-workflow` as a test target).
2. The `quicktask-vikunja` stale reference is a fast, independent fix if
   picked up separately — not a prerequisite for #223.
3. Branch convention: `.worktrees/` off latest `main`, random `wt/<hex>`
   branch name (see `CLAUDE.md` → Git Worktrees). Full verification gate
   before any PR: `gofmt -l .`, `go vet ./...`, `golangci-lint run`,
   `go test -race -cover ./...`, `govulncheck ./...`.
