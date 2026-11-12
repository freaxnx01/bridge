# Cockpit: dispatch verdicts, gates and tick history (#387)

## Intent

The dispatch timer runs in dry-run mode for an observation week (since 2026-11-11).
The operator wants that week to answer one question: **which issues would the
dispatcher have released for implementation, and why those ones?** Today the only
answer is text in `journalctl --user -u bridge-dispatch` or a manual
`bridge dispatch --dry-run`. Dry-run ticks persist nothing, and nothing anywhere
records *why* a picked issue beat the others: the decision table only says
"dispatch".

"Why" has three layers, and the panel has to show all three:

1. **Why it is a candidate:** it passed `dispatch.Eligible`.
2. **Why it ranked where it did:** the ordering ladder in `dispatch.Order`
   (repo priority → milestone due → type → size → age). Not recorded today.
3. **Why it stopped there:** the cap and gate reasons from `ApplyCaps` and
   `PartitionByWindow` (window, budget rung, nightly, global, per-repo, lane).
   Already recorded as `Decision.Reason`.

## Goals

- Every dispatch tick (dry-run and live, timer and manual) appends a record of its
  decisions, including the ranking explanation, to a capped local log.
- A new **Dispatcher** area in the cockpit:
  - **Week summary (top):** one row per issue seen as a candidate in the selected
    period (default 7 days), with how many evaluated ticks would have picked it and
    its most frequent reason.
  - **Tick timeline + tick detail (drill-down):** pick a tick (or click an issue in
    the summary to filter to its ticks) and see the ranked list from that tick:
    rank, verdict, the deciding ranking rule, the skip reason, and the gate figures
    at that moment.
- **Live part**, on the existing Pipeline board:
  - A verdict badge on every Ready card: `next` (would dispatch now) or
    `skip: <reason>`, plus lane and lane-downgrade reason when set.
  - A **gate strip** above the columns: window open or closed (and the next
    opening), budget rung used/allowed %, nightly count/cap, global open PRs/cap,
    paused, last tick time, and each lane's mode (live/dry-run) and count. Same
    figures as `bridge dispatch status`. The Dispatcher area shows the same strip.

## Non-goals

- No actions from the Dispatcher area (no dispatch, approve, or pause buttons).
  Approving plans is #370; the existing Pipeline Dispatch button stays as is.
- No change to what the dispatcher decides. Ordering, eligibility and caps behave
  exactly as today.
- No cross-host or shared storage: the log is a local file on the dispatch host.
- No charts beyond the timeline strip and tables.

## Anti-goals (would be a failure even if it works)

- A dry-run tick changing any dispatcher state (caps, nightly count, budget ledger,
  lane counters other than what dry-run lanes already advance today). The tick log
  is **observability only**: it is never read back as dispatch input.
- A tick failing or exiting non-zero because the log could not be written.
- The live preview and a real tick disagreeing because they run different code.
  Both must go through one shared evaluation function.

## Constraints

- Go stack rules from `CLAUDE.md`: logic lives in `internal/`, `cmd/` only wires;
  no new modules; stdlib `testing` with hand-rolled fakes; `go test -race` green.
- Frontend is the existing Svelte app in `web/` (Vitest tests beside components).
  No new npm dependencies.
- `GET` endpoints are open on the LAN like `GET /api/pipeline`; no new `POST`s.
- #368 (issue inbox) adds `routes/Inbox.svelte` and a Header area in parallel.
  `App.svelte` and `Header.svelte` are shared touch points; keep edits there
  minimal (one area entry) so the two merge cleanly.

## Design (decided)

### 1. Shared tick evaluation in `internal/dispatch`

Move the decision pipeline that `runDispatch` (`cmd/bridge/dispatch.go:262-338`)
assembles inline into one function in `internal/dispatch`, e.g.:

```go
// Evaluate runs one tick's decision path over already-fetched inputs. Pure:
// no network, no state writes. Callers fetch inputs and apply decisions.
func Evaluate(in TickInput) Tick
```

`TickInput` carries the fetched repos (issues, milestones, PRs), config, state,
lane gate, measured budget usage, `now` and whether the tick is window-gated
(`--auto`). `Tick` carries the ordered decisions plus a `Gates` snapshot (window
open/next, rung used/allowed, nightly count/cap, global open/cap, per-repo open
counts, lane modes and counts, paused). `runDispatch` becomes fetch → `Evaluate`
→ render → (log) → apply. The `repoInput`, `collectCandidates`,
`countOpenAgentPRs`, `mergeInOrder`, `partitionForTick`, `laneCounts` helpers move
with it. The CLI output (table, `--json`) stays byte-identical.

`TickInput` has a `WindowGate` mode with two values: `GateNow` (what `--auto`
does today: candidates whose lane is out of window are skipped `outside window`,
and the rung uses the current time) and `GateNextWindow` (used only by the live
preview, see §4: window gating is ignored, and the rung and nightly cap are
evaluated as they will apply at the next window opening). The CLI keeps its
existing early exit for `--auto` with no acting lane *before* calling `Evaluate`
(it saves the network fetch); `Evaluate` itself never early-exits.

`resolveGate` (lane `agent.yml` gate, 6 h file cache) and `measureWindowUSD`
(transcript scan) move from `cmd/bridge` into `internal/dispatch` (or a small
sibling package) so `serve` can call them too. *(Builder-check call: both are
cheap enough for a 10-minute cadence thanks to the existing gate cache.)*

### 2. Ranking explanation

Each decision gains a rank (1-based position in `Order`'s output, over **all**
candidates, gated or not: the ranking is about order, not about who was acted on)
and a **deciding rung**: the first ladder rung on which this candidate differs from
the **next** candidate in that same full order (`repo priority`, `milestone due`, `type`, `size`,
`age`), with both values, e.g. `type: bug vs feat`. The last candidate has no
deciding rung. It also carries its own ladder keys (repo-priority pattern matched,
milestone title and due date, type rank name, size, created date) so the UI can
show them on hover/expand. Implemented next to `Order` in `order.go`, reusing
`repoPriorityRank`, `compareDue`, `typeRank`, `sizeRank`.

`--json` gains `rank`, `rank_reason` and `ladder` fields (additive; existing fields
unchanged). The text table is unchanged.

### 3. Tick log

- File: `~/.cache/bridge/dispatch-ticks.jsonl` (next to `dispatch.json`, via
  `cacheRoot()`), one JSON object per line.
- Record: `{at, mode: "dry-run"|"live", trigger: "auto"|"manual", gates, decisions[]}`
  where each decision is the extended `decisionJSON` shape (repo, issue, title,
  lane, lane_reason, dry_run, dispatch, reason, rank, rank_reason, ladder).
  `mode` is the `--dry-run` **flag** of that tick; a dry-run *lane* is visible per
  decision through the existing `dry_run` field.
- Written by `runDispatch` after rendering, before applying, for every tick that
  reached `Evaluate`. Ticks that exit early (paused, or `--auto` outside every
  window) are **not** logged; they would add ~40 empty lines a day.
- Retention: entries older than 30 days are dropped. Pruning happens on append when
  the oldest line is older than 30 days (rewrite via temp file + rename).
- Append with `O_APPEND` and one `Write` per line. Readers skip a malformed (e.g.
  partially written) line instead of failing.
- A write failure is logged with `slog.Warn` and does not change the tick's exit
  code or decisions.

### 4. Serve side

- **Live preview:** after each issues refresh (10 min) the cockpit collector runs
  `Evaluate` over its collected repo data with `GateNextWindow` (**operator
  decision**: badges answer "what will the next tick inside a window pick", not
  "what would a tick do this second", so they are informative all day). The gate
  strip separately shows the *current* state, e.g. "window closed, opens 18:00".
  The result is kept as a snapshot like the other sources (failure listed in
  `errors` under source `dispatch`, last good snapshot kept). Dispatch config and
  state are re-read on each refresh, so edits to `dispatch.json` show up without
  restarting `serve`.
  - Where the collector skips PR/milestone fetches for a repo (no gate
    candidates), that repo has no candidates either, so the preview is unaffected.
- **Board:** `IssueCard` on Ready gains `verdict` (`"next"`|`"skip"`) and
  `verdictReason`, filled from the live preview by `BuildBoard` (new
  `Input.Preview`). `Board` gains a `gates` object. *(Builder-check call:)* the
  Ready column switches to the real dispatch order, i.e. `dispatch.Order` with
  `cfg.RepoPriority` instead of `nil`, so card order and `next` badges agree.
  Board tests that pin the old order are updated to the new, intended order.
- **New endpoints** (JSON, open GET):
  - `GET /api/dispatch/history?days=7` → `{summary: [{repo, issue, title,
    evaluatedTicks, pickedTicks, topReason}], ticks: [{at, mode, trigger,
    pickedCount, skippedCount, candidates: [{repo, issue, dispatch}]}]}`.
    *(Builder-check calls:)*
    - The **summary counts `trigger: auto` ticks only**: it answers "what would
      the timer have done". Manual ticks still appear in `ticks`, marked.
    - An issue is **evaluated** in a tick when it appears among that tick's
      decisions (any verdict). It is **picked** when `dispatch` is true, which
      includes a dry-run lane's "WOULD dispatch".
    - `topReason` is the most frequent `rank_reason` among picked ticks, else the
      most frequent skip `reason`.
    - `candidates` is small per tick and lets the client filter the timeline by
      issue without another request.
  - `GET /api/dispatch/ticks/{at}` → one full tick record (`at` as RFC3339).
  - The live gates come with `GET /api/pipeline` (above); no separate endpoint.
- The log is read on request (a week is a few hundred lines); no caching needed.

### 5. Frontend

- Header gets a **Dispatcher** area next to Pipeline (`App.svelte` area switch).
- `routes/Dispatcher.svelte`: gate strip, period selector (7 days default, 1 / 7 /
  30), week summary table, timeline strip of ticks (picked count per tick, dimmed
  when 0), tick detail table. Clicking a summary row filters the timeline to ticks
  where that issue was a candidate and opens the latest one.
- `components/GateStrip.svelte` shared by Pipeline and Dispatcher.
- `components/IssueCard.svelte` shows the verdict badge when present.
- Mockup reviewed in brainstorming: week summary (layout B) on top, drill-down to
  the per-tick ranked list (layout A).

### 6. Docs

- `docs/cockpit.md`: Dispatcher area, the new board fields, the new endpoints.
- `docs/dispatch.md`: the tick log (path, retention, what is logged), the new
  `--json` fields, the deciding-rung definition.

## Left to the builder

- Exact Go type and field names, package-file split inside `internal/dispatch`.
- Visual styling of the strip, badges and tables (follow existing cockpit CSS).
- Whether the history endpoint handler lives in `internal/api` next to
  `pipeline.go` or in its own file.
