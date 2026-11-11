# Cockpit "Today" — the morning digest for auto-dispatch

Status: design, approved wireframe and flow (2026-11-11). No code in this
document's session. Wireframe: [`docs/design/cockpit-today/wireframe.md`](../../design/cockpit-today/wireframe.md),
flow: [`docs/design/cockpit-today/flow.md`](../../design/cockpit-today/flow.md).

Builds on: the cockpit design ([`2026-10-24-bridge-cockpit-design.md`](2026-10-24-bridge-cockpit-design.md)),
the issue-centric inbox (#368, spec
[`2026-11-11-issue-inbox-design.md`](2026-11-11-issue-inbox-design.md);
write actions #374, blocked sessions #375), pipeline-truth `NextAction` (#367,
merged 2026-11-11, spec
[`2026-11-10-pipeline-truth-next-action-design.md`](2026-11-10-pipeline-truth-next-action-design.md)),
the run marker (agent-workflow ADR-021), and the quota throttle (#371).

## Intent

Auto-dispatch now runs unattended. The timer was enabled on 2026-11-11, and
for the observation week it is forced to `--dry-run`. The operator wants to
see **what auto-dispatch did** without reading `journalctl` or opening
sessions. The moment this page owns is the **morning check: "what happened
overnight?"** From that page the operator wants to **merge the PRs that are
ready and retry the runs that stopped**, without leaving it.

"Next level" for the cockpit, in the operator's words, is:

1. Seeing what auto-dispatch did.
2. Acting on it from the same page.
3. Later, an all-repos overview with drill-down (outlined here, designed
   later).

## The trust rule (anti-goal)

> If it says nothing happened without saying why, the page has failed, even
> when everything on it is technically correct.

Consequences, all binding:

- **Every tick leaves a record**, including ticks that dispatch nothing. Each
  record carries the reasons: outside window, nothing eligible with skip
  counts by reason, paused, budget rung, lane downgrade, dry-run "would have
  dispatched".
- **Missing ticks are shown.** The timer fires every 30 min (`docs/dispatch.md`,
  "Systemd timer and service"), so the digest knows which ticks to expect. A
  gap is a red banner at the top, e.g. "no dispatcher tick 23:30–02:00 (5
  expected, 0 seen)".
- **Inert ticks are flagged.** A tick that ran but saw 0 repos, the documented
  "green timer doing nothing" case (no GitHub client), is a warning, not "0
  dispatched".
- **Every unsupported or failing source says so once, quietly.** One red line
  per ADO repo (#366) is the opposite failure: so much noise that the real
  reason disappears.

The cockpit's original anti-goals still hold: slow, stale, noisy.

## Goals

- The cockpit start page (#368's Inbox, extended) answers, within five seconds on a phone: what
  happened since I last looked, what needs me, and why nothing else happened.
- **Merge** a ready agent PR from the page, with the agent's summary and the AI
  review verdict visible and the diff one tap away.
- **Retry** a stopped run from the page, using #374's actions (Retry, Retry
  with, Park) on the inbox cards.
- A persistent, readable **dispatch journal**, which the observation week needs
  right now.

## Non-goals

- **The WebUI is never the engine.** Dispatch runs from the systemd timer
  (`bridge dispatch --auto`) and keeps running when `bridge serve` is down. The
  page reads state and triggers explicit, confirmed actions; it never decides
  what gets dispatched.
- No second inbox. **Needs you** is #368's inbox, embedded. The digest
  sections hold only what changed in the window.
- No in-browser code review with comments back to the agent. The diff is
  read-only in v1.
- No plugin API (no consumer for it yet).
- No chat UI. Drill-down into a live session stays "Open in Claude" (RC),
  as #368 decided.
- No redesign of #368's cards, issue detail, Retry-with picker or Park.

## Constraints

- Stack and conventions are those of the existing cockpit: Svelte 5 + Vite in
  `web/`, embedded in `bridge serve`; Go stdlib `net/http`; collectors with
  last-good snapshots; SSE for change notification; no per-request forge
  fan-out (the diff endpoint is the single lazy exception).
- Writes: bearer token (`BRIDGE_API_TOKEN`) + explicit Origin allowlist +
  confirm dialog naming the exact effect, the same as Enrich and Dispatch today.
- Phone first, desktop usable.
- Sequencing: #367 merged on 2026-11-11. #368 (read-only inbox, spec landed
  2026-11-11) reworks the cockpit next. Every issue below that touches
  `internal/cockpit` or `web/` starts after #368 has merged. The journal
  (issue 1) only touches `internal/dispatch` and can start now.

## The page (decided with the operator)

See the wireframe for layout and the flow for behaviour. In short:

- **One start page (decided):** Today **extends #368's Inbox page**. It does
  not add a page of its own. #368 makes Inbox the default area. This design
  adds the summary strip and tick-health banner above #368's cards, and the
  overnight sections below them. The wireframe's `[Today]` tab label stands
  for that Inbox entry. The Pipeline board stays as it is.
- **Summary strip:** "Since <anchor>" with presets (last visit, default; since
  18:00; 24 h; 7 d). Counts: dispatched · PRs opened · merged · stopped · spend.
  Quota line from #371.
- **Tick health banner:** gaps and inert ticks, only when present.
- **Needs you:** #368's inbox cards, plus the new **Merge card**.
- **Done · Still going · Didn't happen:** one line per issue with the story
  compressed (`dispatched 02:00 → failed task_failure → retried 03:00 → PR
  #374 ✓`). An issue that is also a Needs-you card shows only `→ in Needs you`.
- **Full journal:** every tick, newest first, empty ticks folded, dry-run ticks
  marked.

### Merge card: what the operator needs to trust Merge

Decided: the **agent's own summary** and the **AI review verdict**; the diff
"maybe useful in some cases", so it sits behind a tap.

| Evidence | Source (verified on PR #373) |
|---|---|
| Agent summary | PR body, `## Summary` section (pipeline-written) |
| AI review verdict | `github-actions` comment starting `<!-- review-pr:<sha> -->`, line `verdict: \`approve\|request_changes\``; a follow-up comment states "self-fix exhausted… stays draft" |
| Checks | PR check runs / statuses |
| Files / diff | PR files with +/− counts; unified diff on demand, read-only |

**Which PRs (decided):** only PRs opened by an `ai-implement` run on a repo
that needs a human merge (`ai-review-human-merge`). PRs that an autonomous
lane merges itself appear under Done only. This keeps the new PR collector
limited to pipeline PRs in human-merge repos.

Merge renders only when checks are green, the verdict is `approve` **for the
current head SHA** (`<sha>` in the review marker equals the PR head), and the
PR is not a draft. Otherwise the slot shows the reason. The server repeats
every check on POST against the `headSha` the client saw and refuses with
`409` + reason if anything moved.

**Identity and protection (verified 2026-11-11):** bridge's `main` requires
2 status checks and **no** approving reviews, and admins are not enforced.
PR #369 was merged by `freaxnx01` with zero reviews. Merge therefore uses the
owner's forge token that bridge already resolves per owner, and it waits for
the required checks. In a repo with stricter protection, GitHub's refusal
(405/403) is shown as the reason on the card. Nothing is bypassed.

**Merge method and branch:** squash (allowed on bridge; the branching rules
say rebase or squash). `delete_branch_on_merge` is off on bridge, so the
action deletes the head branch explicitly after a successful merge.

## The new data source: dispatch journal

Today a tick's decisions go to `journalctl --user -u bridge-dispatch` and
`--json` stdout only. `internal/dispatch/state.go` keeps counters, not history.
The digest needs one record per tick, written by the tick itself:

- tick time, mode (live / dry-run), lane(s) in window, repos seen;
- per decision: repo, issue, title, decision (dispatched / would dispatch /
  skipped), reason, lane, downgrade reason;
- tick-level outcome when nothing was decided (outside window, paused, budget
  rung with its reading).

**What counts as a tick (author's call):** every `bridge dispatch`
invocation writes a record tagged with its trigger: `timer` (`--auto`) or
`manual` (`now`, CLI `--dry-run`). Only `timer` records fill the expected
slots. Expected slots are the timer's `:00` / `:30` heartbeat, taken from the
unit's schedule (`docs/systemd/bridge-dispatch.timer`) rather than hard-coded
where practical. They start at the later of the "since" anchor and the first
journal record, so a fresh install never shows a false gap.

**Host:** `bridge-dispatch.timer`, `bridge-serve.service` and
`bridge-mcp.service` all run on the same host (srvdmsk8s01, verified
2026-11-11), so the journal is a local file. Running serve on another host is
out of scope.

**Digest scope (decided):** the digest shows **all pipeline activity** in the
window, not only what the timer started. It joins on run markers and PRs as
well as journal records, and tags each item `dispatched by: timer / manual /
session`. Autonomous self-merges count as merged and show under Done.

Format, location and retention are the builder's call within these bounds:
append-only, local to the dispatch host, written atomically per record,
bounded in size (e.g. last 30 days), and readable by `bridge serve` without
locking the tick. A JSON-lines file under the bridge state/cache directory fits
the existing style (`store.AtomicWrite`, `~/.cache/bridge/*.json`).

## Outline: drill-down (designed later)

Approved as the next step after Today, not designed here:
`Repos (all forges) → repo → milestone → issue → runs / PR / sessions`. The
issue level is #368's issue detail. #215 (milestone hierarchy) and #242
(milestone state, "awaiting manual test" distinct) are its middle levels and
get folded into its design. It gets its own `/ui:brainstorm` → `/ui:flow`.

## Existing issues: how they map

| Issue | Proposal |
|---|---|
| #325 mobile "Needs me" overview | **Close as superseded** by #368 (inbox) + Today (digest). Its concrete gap (PRs waiting 10+ days in `ai-review-human-merge`) is covered by the Merge card and Needs you. |
| #366 ADO error flood | **Prerequisite** for Today (trust rule). Keep; add to the Today milestone. |
| #215 milestone hierarchy, #242 milestone state | **Keep**, link to the drill-down outline; designed with it. |
| #179 WIP cap pull-gate | **Keep as roadmap**; relates to #370 (approval policy). Comment linking both. |
| #168 "What next?", #169 stale-issue nudge | **Keep as roadmap**; not part of Today. |
| #201 ideas from another tool | **Keep**; comment that idea 8 (precise danger-zone copy) is applied in the confirm dialogs here. |
| #320 weekly usage bound | Dependency of the quota line, via #371. |

## Proposed new issues (sequenced)

1. **`feat(dispatch): write a per-tick dispatch journal`** — Every `bridge
   dispatch` tick (live and dry-run) appends one record with its decisions and
   reasons, including empty ticks. Unblocks reviewing the observation week
   outside `journalctl`. *Depends on:* none (#367 merged); coordinate with
   #253 (same tick path). *Milestone:* AutoDispatch.
2. **`feat(serve): GET /api/digest — digest of dispatch and pipeline activity since a timestamp`**
   — Joins journal, run markers and PR state per issue. Computes expected vs.
   seen ticks and flags inert ticks. No per-request forge fan-out. *Depends on:*
   1, #368 (issue/run collector). *Milestone:* Cockpit Today (new).
3. **`feat(webui): extend the Inbox with the overnight digest`** — Adds to
   #368's start page the summary strip, tick health banner, Done / Still going
   / Didn't happen sections, the journal view and every state in the
   wireframe. Needs you is #368's card list. *Depends on:* 2, #368 UI.
   *Milestone:* Cockpit Today.
4. **`feat(serve): merge agent PRs from the cockpit behind a head-SHA guard`**
   — Merge card (summary, review verdict, files) + guarded `POST` that
   re-checks head, checks, draft and mergeability. Adds a `merge` kind to
   #368's `BuildInbox`: today a successful run (`ai:done`) is `ActionNone` and
   is dropped, so a PR awaiting a human merge never becomes a card. Candidate
   rule: `ai:done` + an open pipeline PR in an `ai-review-human-merge` repo.
   Urgency group 1 (work waits for review). Not part of #374 (Retry,
   Retry with, Park); it reuses #374's confirm/guard pattern. *Depends on:* 3.
   *Milestone:* Cockpit Today.
5. **`feat(webui): read-only PR diff in the merge card`** — Lazy per-click diff
   endpoint + collapsed per-file view, cached per head SHA. *Depends on:* 4.
   *Milestone:* Cockpit Today.
6. **`feat(webui): show the usage quota line in the Today header`** —
   Renders #371's meter (weekly + 5 h, #320) in the summary strip. *Depends
   on:* #371, 3. *Milestone:* Cockpit Today.
7. **`feat(webui): clear a stuck enrichment lock from the board`** — "Stuck
   enriching" cards get *Clear lock* / *Clear & re-enrich* (confirmed, guarded).
   Seen live on 2026-11-11: BI-ArchiveUploader #612/#614 held a dead lock for
   20 days. *Depends on:* #368 (cockpit rework). *Milestone:* Cockpit Today.
8. **`feat(review): add a machine-readable verdict marker to the AI review comment`**
   (repo: freaxnx01/agent-workflow) — Same idea as ADR-021 for the review
   comment (verdict, head SHA, blocking count), so 4 does not parse prose.
   *Depends on:* none; 4 can ship on the regex fallback. *Milestone:*
   agent-workflow's choice.
9. **`feat(webui): drill-down from all repos to issue (repos → milestone → issue)`**
   — Roadmap placeholder for the next design. Folds #215/#242 in. *Depends on:*
   #368 issue detail. *Milestone:* Roadmap.

Order: 1 only touches `internal/dispatch`, but it hooks the same tick path
that #253 (`--retry-only`) and #374's re-dispatch change, so coordinate it
with #253 rather than run them in the same wave. 2–5 follow #368, with 6 and 7 in parallel. 8 is
independent. Retry on the digest comes with #374 (which depends on #253);
Today does not wait for it, and shows Open PR / Open run until #374 lands.

**Enrichment timing (author's call):** only 1, 7 and 8 can be enriched now.
2–5 depend on #368's collector, cards, Retry endpoint and its "stopped"
semantics. Those exist only as the #368 spec so far. They are enriched after #368 merges, so their
plans don't have to guess #368's interfaces.

## Left to the builder

- Endpoint names and JSON shapes (document in `docs/cockpit.md`).
- Journal format, path and rotation within the bounds above.
- How Today's collector reuses #368's issue/run collector versus adding its
  own; refresh intervals; rate-limit budget for PR files and checks.
- Where the "since" anchor is stored (browser storage wrapped in try/catch is
  enough; it's per viewer).
- Exact wording of reasons, as long as every "nothing" has one.

## Calls made by the author (not decided by the operator)

- Merge visibility rule (green + approve for head SHA + not draft) and the
  server-side `409` re-check.
- Outcome grouping order: needs you, done, still going, didn't happen.
- "Since" defaults to last visit, fallback 18:00 yesterday; stored when the
  page is left or hidden.
- Spend line from run-marker `cost_usd`; quota from #371.
- Diff read-only in v1.
- Tick definition (trigger tag, expected slots from the timer, start of the
  expected range).
- Enrichment timing: 2–5 only after #368 merges.
- Merge deletes the head branch explicitly.
- New milestone name "Cockpit Today".
- A card for a private repo could hint which forge account it needs. Minor:
  noted, not an issue (the 2026-11-11 404 was a logged-out browser).
