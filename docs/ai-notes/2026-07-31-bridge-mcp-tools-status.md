# Bridge MCP cross-forge tools — status

_2026-07-31. Tracks #220, #219, #221, #223. Worktree: `.worktrees/new-mcp-cmds`._

## Original scope (4 tools) — all done

A multi-session plan to grow bridge's MCP surface: `list_tree` (#220),
`update_repo` (#219), `search_code` (#221), and tree-writes/PR-opening (#223).
Sessions were explicitly sequenced — do not start #221 or #223 until earlier
ones land and their open questions are settled. All four are now merged to
`main`; the plan is complete.

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
- **#223 `put_file`** — PR #228, merged. Create-or-update a file directly on
  a repo's default branch (no branch/PR), gated by a server-side path
  allowlist (default `docs/**/*.md` + root `*.md`, configurable via
  `--put-file-allowlist`/`BRIDGE_MCP_PUT_FILE_ALLOWLIST`; `.github/**` always
  denied). `sha` is required to update an existing file, checked proactively
  via `GetFile` rather than left to a raw forge-API error. Owner-scoped, no
  per-repo allowlist — same trust boundary as `create_repo`. Resolved the
  "Blocked" repo-allowlist question below as option 2. Two real security
  issues were found and fixed during implementation (not just theoretical
  hardening): a path-traversal allowlist bypass (`docs/../.github/...`), and
  a URL-metacharacter bypass in `GithubClient.PutFile` (`?` in a path let a
  write silently retarget an existing root file like `justfile`/`.envrc`).
  Both are closed with regression tests. Live-verified against
  `freaxnx01/agent-action-sandbox` on GitHub; no Forgejo sandbox repo exists,
  so Forgejo coverage is unit-test only (`ForgejoClient.PutFile`). Follow-up
  minors (doc staleness, an un-audited denial path) filed as #229; a
  dispatch-side test-coverage gap found during unrelated PR review filed as
  #230.

All four tools follow the same conventions: capability interfaces in
`internal/mcp/tools.go` (`treeLister`, `repoUpdater`/`topicsSetter`,
`searchCoder`, `fileWriter`), fan-out-with-warnings for multi-owner reads
(mirrors `list_repos`), draft-first `confirm` gate for writes, live-verified
against real repos before opening each PR.

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

## Resolved — repo-allowlist policy (#223)

The "which repos can `put_file` write to" question below was decided as
**option 2**: owner-scoped, no per-repo allowlist — same trust boundary as
`create_repo` (any repo under the configured default owners). The path
allowlist (which *paths* within a repo) is the actual safety control, not a
repo list. Decision recorded on #223's issue comments before implementation.

## Remaining loose end

- `freaxnx01/quicktask-vikunja:.github/workflows/claude.yml:43` still says
  `freaxnx01/claude-pipeline@main` (discovered while live-testing
  `search_code`, never part of #220/#219/#221/#223's scope). Still open as of
  this update — a fast, independent fix whenever it's picked up.

## If starting new MCP-tool work

Follow the same conventions this batch established: capability interface in
`tools.go`, draft-first `confirm` gate for writes, live-verify on a
designated sandbox repo before opening the PR (do not use `freaxnx01/bridge`
or `freaxnx01/agent-workflow` as a test target). Branch convention:
`.worktrees/` off latest `main`, random `wt/<hex>` branch name (see
`CLAUDE.md` → Git Worktrees). Full verification gate before any PR:
`gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test -race -cover ./...`,
`govulncheck ./...`.
