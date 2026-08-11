# Glossary

Shared vocabulary for bridge and the surrounding Software Factory. Seeded
2026-08-11 with enrichment-run terms; extend as other areas need it.

## Enrichment run

A batch of parallel Claude Code sessions that turn scoped ideas into
implementation-ready issues across a repo group (`game-*`, the Software Factory
repos). Produces issues labelled `enrichment` plus a shared `run:<date>` label.
Distinct from an implementation run, which consumes those issues.

See: `docs/ai-notes/2026-08-11-ops-enrichment-runs.md`

## Quick-mode

A Superpowers interview variant that asks the human nothing. At each decision
point the agent takes the option it would have recommended, records it in the
issue's assumptions block, and continues. Escalates only on one-way doors.
Converts a synchronous interview into an async review queue.

## One-way door

A decision quick-mode refuses to make alone: irreversible operations, anything
touching credentials, anything that spends money. These escalate to the human
and block the session. Everything else is a two-way door — reversible, so the
agent decides and records.

## Assumptions block

An `## Assumptions` section in a quick-mode issue body. One entry per decision
the agent made unaided, each carrying the rejected alternative and a confidence
marker. The unit of async review: the human scans low-confidence entries first.

## Attention state

Ephemeral, machine-local record of which session is blocked on human input.
Written by Claude Code hooks (`bridge-hooks/notify.sh` on `idle_prompt` and
`elicitation_dialog`, `clear-idle.sh` to clear), read by `bridge sessions` and
the presence layer. Lifetime is the box — it does not survive a reboot and is
never persisted to a forge.

## Agent-owned body

Convention that an enrichment issue's body is written only by agents, never
hand-edited. Human corrections go in as comments. Keeps revision idempotent —
the body is regenerated wholesale from scope plus comments — and makes the
comment thread the audit trail for why a plan says what it says.

## Revise

Regenerating an enrichment issue's body from its scope and all its comments.
A fresh session, not a resumed one: the issue is the entire input. Wholesale
regeneration rather than delta patching, so repeated runs on unchanged input
produce the same body.

## Run label

`run:<date>` — groups issues from one enrichment run across repos. Needed because
milestones are per-repo on both GitHub and Forgejo and so cannot span a repo
group. Paired with a tracking issue in `agent-workflow` holding a child checklist.

## Slot

A numbered launch position for an agent session, with an associated repo,
worktree, and tmux session. Managed by `cmd/bridge/slots.go`; the unit that
`bridge sessions` reports on and that hooks are parameterised by.
