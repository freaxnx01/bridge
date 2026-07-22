# MCP: mutating + lifecycle tools (close/update/label/comment, archive, gated delete)

Date: 2026-07-22
Status: approved, not yet implemented
Depends on: `2026-07-20-mcp-capability-interfaces-design.md`, `2026-07-20-mcp-list-issues-forges-create-repo-design.md`

## Problem

The Bridge MCP surface is read-heavy plus two draft/confirm creators
(`create_issue`, `create_repo`). There is no way to mutate an existing issue
and no lifecycle operations at all. This blocks #166, #167, #169, and the
acceptance-criteria handoff pattern used in `SaveOutlookCalendar`.

## Step 0 — API surface verification

Confirmed against docs.github.com (REST + GraphQL reference) and
docs.gitea.com/api v1.23 (Forgejo is a Gitea-API-compatible fork):

| Operation | GitHub | Forgejo/Gitea |
|---|---|---|
| Close issue | `PATCH /repos/{owner}/{repo}/issues/{issue_number}` `{"state":"closed"}` (optional `state_reason`: `completed`/`not_planned`/`duplicate`) | `PATCH /repos/{owner}/{repo}/issues/{index}` `{"state":"closed"}` (no `state_reason`) |
| Update title/body | same PATCH, fields `title`/`body` | same PATCH, fields `title`/`body` |
| Add labels | `POST /repos/{owner}/{repo}/issues/{issue_number}/labels` `{"labels":[...]}` | `POST /repos/{owner}/{repo}/issues/{index}/labels` |
| Comment | `POST /repos/{owner}/{repo}/issues/{issue_number}/comments` `{"body":"..."}` | `POST /repos/{owner}/{repo}/issues/{index}/comments` |
| Delete issue | **not supported via REST.** Only GraphQL `deleteIssue`, gated by personal-owner-or-org-admin *and* an org-level "allow deleting issues" toggle that is off by default | **not supported at all**, REST or otherwise — only comments/labels/reactions on an issue can be deleted, never the issue itself |
| Archive repo | `PATCH /repos/{owner}/{repo}` `{"archived": true}` (repo admin) | `PATCH /repos/{owner}/{repo}` `{"archived": true}` — same field name |
| Delete repo | `DELETE /repos/{owner}/{repo}` (requires `delete_repo` scope + admin) | `DELETE /repos/{owner}/{repo}` (requires repo admin) |

**Finding that changes the issue's scope:** the issue lists `delete_issue` as
"Forgejo only," assuming GitHub is the one missing REST support. In fact
neither forge can delete an issue at all — GitHub only through a
heavily-gated GraphQL mutation, Forgejo not at all. **`delete_issue` is
dropped from this design.** Building a tool with zero viable implementations
provides nothing; the finding itself is the useful output of Step 0.

## Non-goals

- `delete_issue` (see finding above).
- Tier 2 (`list_milestones`, `list_prs`, `search_issues`) and tier 4
  (`delete_repo`) implementation. Both are specced here for architectural
  consistency but implemented as separate follow-up issues — see Rollout.
- Wiring GitLab/ADO clients into MCP (existing non-goal, unchanged).
- A generic audit-log query/reporting tool. This design only writes entries.

## Guardrails (apply to every tool below)

- New `Deps.AllowDestructive bool`, wired like `ReadOnly` today: CLI flag
  `--allow-destructive` (default `false`), env override
  `BRIDGE_MCP_ALLOW_DESTRUCTIVE=1`. Unlike `ReadOnly`, this gates at
  **handler-call time, not registration** — `archive_repo`/`delete_repo` stay
  registered and visible so `list_git_forges` can report the gate state
  without probing, but the handler itself refuses when the flag is off.
- Every mutating tool keeps the `confirm=false` draft default already
  established by `create_issue`/`create_repo`: build the draft output from
  input, return it without any network call or audit entry when
  `!in.Confirm`.
- `delete_repo` additionally requires `name_confirmation string`, which must
  equal the repo name exactly (mirrors the GitHub web UI's delete
  confirmation). `confirm=true` with a mismatched or missing
  `name_confirmation` is a refusal, not a deletion.
- Every call that actually mutates (`confirm=true`, past the
  `AllowDestructive`/`name_confirmation` gates) appends one audit entry.
  Drafts and refusals are also logged — refusals as their own `outcome`, so
  the trail shows attempted-but-blocked destructive actions.
- `list_git_forges` reports `allow_destructive` alongside the existing
  `read_only`, and its `capabilities` list already omits ungated tools when
  `read_only` is set (existing `isWriteTool` mechanism) — this design adds
  the new tool names to that switch.

## Audit log

New package `internal/audit`. One type:

```go
type Entry struct {
    Time    time.Time
    Forge   string
    Owner   string
    Repo    string
    Tool    string
    Confirm bool
    Outcome string // "success" | "error" | "refused" | "refused_name_mismatch"
}

type Logger struct{ /* wraps *slog.Logger over a JSON handler */ }

func Open(path string) (*Logger, error) // opens/creates path in append mode
func (l *Logger) Log(e Entry)
```

Path resolution at startup (`cmd/bridge/mcp.go`), same precedence style as
`ReadOnly`: `$BRIDGE_AUDIT_LOG_PATH` env var, else
`$XDG_STATE_HOME/bridge/audit.jsonl`, else `~/.local/state/bridge/audit.jsonl`.
One JSON object per line via `slog.NewJSONHandler`. Injected into
`imcp.Deps` as `Audit *audit.Logger`; every mutating handler calls
`d.Audit.Log(...)` as its last step before returning, on every path
(success, forge error, refusal).

## Tool contracts — tier 1 (this round's implementation)

All four resolve the client via `Deps.ClientFor`, type-assert their
capability interface (`Capabilities()` gains a matching entry each), and
follow the create_issue/create_repo draft shape exactly.

### `close_issue`

| Field | Type | Meaning |
|---|---|---|
| `forge`, `owner`, `repo` | string | target repo |
| `issue_number` | int | issue to close |
| `state_reason` | string, optional | GitHub only: `completed`/`not_planned`/`duplicate`; ignored on Forgejo (documented in the tool's jsonschema description, not an error) |
| `confirm` | bool | draft default |

Capability: `issueCloser.CloseIssue(ctx, owner, repo string, number int, stateReason string) (forge.Issue, error)`.
Own response struct declaring `state`, `closed_at`/`updated_at` explicitly —
see Bug fix below for why this matters.

### `update_issue`

| Field | Type | Meaning |
|---|---|---|
| `forge`, `owner`, `repo`, `issue_number` | — | target |
| `title` | string, optional | new title |
| `body` | string, optional | new body |
| `confirm` | bool | draft default |

At least one of `title`/`body` required — validated at the boundary before
any network call (empty-both is a request error, not a no-op network call).
Capability: `issueUpdater.UpdateIssue(ctx, owner, repo string, number int, title, body *string) (forge.Issue, error)`.

### `add_labels`

| Field | Type | Meaning |
|---|---|---|
| `forge`, `owner`, `repo`, `issue_number` | — | target |
| `labels` | []string | labels to add (non-empty required) |
| `confirm` | bool | draft default |

Capability: `labelAdder.AddLabels(ctx, owner, repo string, number int, labels []string) ([]string, error)`
returning the issue's full label set after the call.

### `comment_issue`

| Field | Type | Meaning |
|---|---|---|
| `forge`, `owner`, `repo`, `issue_number` | — | target |
| `body` | string | comment body |
| `confirm` | bool | draft default |

Capability: `issueCommenter.CommentIssue(ctx, owner, repo string, number int, body string) (forge.Comment, error)`.
New `forge.Comment{ID int, Body string, Created time.Time}` return type.

## Tool contracts — specced, not implemented this round

### Tier 2 — `list_milestones`, `list_prs`, `search_issues`

Read tools. `list_milestones`/`list_prs` mirror `list_issues`'s single-repo
shape (`{forge, owner, repo}` → list). `search_issues` is cross-repo, which
doesn't fit the per-forge-client capability model used everywhere else —
needs its own design pass on how it fans out across `Deps.DefaultOwners`
before a plan is written for it. Flagged here, not resolved.

### Tier 3 — `archive_repo`

| Field | Type | Meaning |
|---|---|---|
| `forge`, `owner`, `repo` | — | target |
| `confirm` | bool | draft default |

Gated by `AllowDestructive`. Capability: `repoArchiver.ArchiveRepo(ctx, owner, repo string) (forge.RepoRef, error)`,
PATCH `{"archived": true}` on both forges (reversible — the guardrail is
`AllowDestructive`, not a separate unarchive step in this design).

### Tier 4 — `delete_repo`

| Field | Type | Meaning |
|---|---|---|
| `forge`, `owner`, `repo` | — | target |
| `name_confirmation` | string | must equal `repo` exactly |
| `confirm` | bool | draft default |

Gated by `AllowDestructive` **and** `name_confirmation`. Capability:
`repoDeleter.DeleteRepo(ctx, owner, repo string) error`, `DELETE
/repos/{owner}/{repo}` on both forges.

## Bug fix: `updated`/`updated_at` zero-value

Already fixed in `313b0d2` (today, prior to this issue) — `CreateIssue` and
`CreateRepo` on both forge clients now declare `UpdatedAt` explicitly in
their response structs instead of dropping it via an anonymous struct that
never named the field. This design's regression-test task
(`internal/forge/github_test.go`, `forgejo_test.go`) covers it: fake HTTP
server returns a non-zero `updated_at`, assert the returned `Issue`/`RepoRef`
carries it through. Every new response struct in this design (close, update,
label, comment) is declared with its needed fields named explicitly from the
start, avoiding a repeat of the same class of bug.

## Interface changes

`internal/mcp/tools.go` gains four tier-1 capability interfaces
(`issueCloser`, `issueUpdater`, `labelAdder`, `issueCommenter`) plus stub
declarations (not implementations) for tier 3/4's `repoArchiver`,
`repoDeleter` so `Capabilities()` has a complete switch even before those
tiers land — unimplemented capabilities simply never match on the concrete
clients yet.

`internal/forge/github.go` and `forgejo.go` each gain `CloseIssue`,
`UpdateIssue`, `AddLabels`, `CommentIssue` methods (tier 1 only this round),
following the existing hand-rolled `get`/`post` HTTP helper pattern — no new
dependencies, no SDK.

## File layout

Tier-1 handlers go in the existing `internal/mcp/tools_write.go`
(established split from `6ccf42a`), grouped after `create_issue`/`create_repo`.
Their I/O types live alongside. `internal/audit` is a new top-level package
under `internal/`, no MCP dependency (importable by `cmd/bridge/mcp.go`
directly for wiring).

## Testing

Table-driven, hand-rolled fakes extending `fakeForge` in `tools_test.go`
with the four new capability methods. Per tool:

1. `confirm=false` → draft output, zero fake calls, zero audit entries.
2. `confirm=true` happy path → calls through, `outcome=success` logged,
   `Draft: false` in output.
3. Forge lacking the capability → forge-asymmetry error naming the forge.
4. (tier 3/4 only, specced not implemented) `AllowDestructive=false` →
   refusal, zero network calls, `outcome=refused` logged.

`internal/audit`: `Logger.Log` writes one valid JSON line per call,
append-only across multiple opens of the same path.

Regression test for the `updated_at` bug per Bug fix above.

Gate before merge: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`,
`go test -race ./...` and `just test` green on both forges.

## Rollout

Implement tier 1 + guardrail infra (`AllowDestructive`, `internal/audit`) +
the bug-fix regression test in this issue's plan. Stop for review per the
issue's Process section. File separate GitHub issues for tier 2
(milestones/PRs/search) and tiers 3–4 (archive/delete_repo) before
implementing them, referencing this spec as their shared design.
