# ops: enrichment runs and async review

Status: idea / pre-spike. Not a design. Promote to `docs/specs/` only after the
spike below returns a yes.

## Problem

Running several Claude Code sessions in parallel on agent-dev to enrich issues
across repo groups (`game-*`, the Software Factory repos) means tab-hopping to
find which session is blocked on a Superpowers interview question. The human is
the serial resource, so N parallel interactive interviews do not actually
parallelise.

## The reframe

A dashboard that tells you which session is waiting still requires you to be
waiting. The primary fix is not a better attention router — it is removing most
of the synchronous questions. Quick-mode is the load-bearing piece; session
routing is the exception path.

## What already exists in bridge

Checked against the tree on 2026-08-11. Most of the attention-state layer is
already built, which was not obvious from outside:

- `bridge sessions` / `bridge sessions attach` — slot, state, age; attach by slot.
- `cmd/bridge/presence.go`, `presence_write.go` — presence tracking.
- `cmd/bridge/slots.go` — slot architecture, pruning.
- `bridge-hooks/notify.sh` — Claude Code `Notification` hook; on `idle_prompt`
  touches `~/.cache/bridge/sessions/<slot>.idle-since`, debounced 120s; on
  `elicitation_dialog` pages immediately via Telegram.
- `bridge-hooks/clear-idle.sh` — clears the idle marker.
- `cmd/bridge/dispatch.go`, `internal/dispatch/` — eligibility, ordering, failure
  handling for dispatched work.
- `bridge-bot` — Telegram out-of-band access, presence-aware paging.

So "which parallel session needs input" is largely solved at the state layer.
What is missing is a single verb that consumes it.

## Proposed shape

Nothing here needs a new binary or a new repo.

1. **Quick-mode** — a skill/preamble variant in `ai-instructions`, propagated via
   `/sync-ai-instr`. Rules: ask nothing; at each decision point take the option
   you would have recommended; record it under `## Assumptions` with the rejected
   alternative and a confidence marker; escalate only on one-way doors
   (irreversible ops, credentials, anything that spends money).

2. **`bridge next`** (or a tmux keybind with no command name at all) — read
   `bridge sessions --json`, pick the oldest slot in a waiting state, run
   `bridge sessions attach`. The 90% use is `prefix + n` plus a count in
   `status-right`, not a typed command.

3. **`agp enrich <glob>`** — spawn N detached quick-mode sessions, each creating
   an issue via the bridge MCP tools with labels `enrichment` and `run:<date>`.
   Lives next to `ai-implement` in `agent-workflow`, not here.

4. **`agp revise`** — wholesale regeneration of the issue body from scope plus
   every comment. Not delta patching: idempotent, no cross-round drift, single
   writer so no append race.

## Where the human answers

Today: in the tmux pane, synchronously, mid-interview.
Proposed: in the issue thread, asynchronously, after the run completes.

Loop: enrich → review issues in one queue (Forgejo UI, `bridge issues`, or
`gh issue list -l enrichment`) → corrections go in as **comments** → revise →
flip label to `ready` → `ai-implement` picks it up.

Convention worth enforcing: the issue **body is agent-owned, humans comment
only**. Hand-editing the body gets clobbered by the next revise run. Comments
double as the audit trail for why a plan says what it says.

## Grouping

Milestones are per-repo on both forges, so a run spanning 30 `game-*` repos
cannot use the milestone primitive. Use a `run:<date>` label plus a tracking
issue in `agent-workflow` with a child checklist.

## Sequencing — spike first

The whole design rests on one untested assumption: that a quick-mode assumptions
block is reviewable async. If it is not, the async model collapses and the
attention router becomes the primary tool instead of the exception path.

1. **Spike, no code.** One `game-*` repo. Run Superpowers brainstorm→spec with a
   hand-pasted no-questions preamble. Paste the result into an issue by hand.
   Acceptance: you can accept, correct, or reject from the issue alone, without a
   terminal. Timebox: one evening.
2. Quick-mode skill in `ai-instructions`.
3. `bridge next` + tmux keybind (state layer already exists).
4. `agp enrich`.
5. `agp revise`.

## Open questions

- Does `bridge sessions` state distinguish "waiting for input" from "idle"
  precisely enough for `next` to pick correctly, or does `next` need to read the
  `.idle-since` markers directly?
- Escalation rate: if quick-mode escalates in more than roughly one run in ten,
  the one-way-door rule is too broad and needs tightening.
- Revision loop cap: after ~3 rounds an issue is not a revision problem —
  escalate to a live interactive session.
- If a correction invalidates the premise (wrong framework, wrong scope), close
  and re-enrich rather than revise; a patched-around plan is worse than a fresh one.

## Known tradeoff

Quick-mode loses mid-interview steering. Today you can catch a wrong turn at
question three; with quick-mode you get a finished plan built on a bad premise
and have to reject it. The assumptions block with confidence markers is the
mitigation — scan low-confidence entries first — but it is a real cost and it is
why the escalation rule needs care.

## bridge gaps this would surface

- `list_issues` is single-repo; a 30-repo enrichment sweep costs 30 calls. An
  issue search filtered by owner + label would collapse that.
- Appending an implementation plan via `update_issue` is a full-body overwrite,
  which races if two agents touch the same issue. Section-anchored patching would
  make the plan step safe under parallelism. (Mitigated for now by the
  single-writer regeneration rule above.)
