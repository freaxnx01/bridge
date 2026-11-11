# Per-tick dispatch journal — design (#379)

Parent design: `docs/superpowers/specs/2026-11-11-cockpit-today-design.md`,
section "The new data source: dispatch journal". Consumer: #380
(`GET /api/digest`). Coordinates with #253 (`--retry-only`).

## Why

A tick's decisions go only to `journalctl --user -u bridge-dispatch` and to
`--json` stdout. `internal/dispatch/state.go` keeps counters (`LastTick`,
nightly and lane counters), not history. The cockpit's overnight digest must
never say "nothing happened" without saying why, so every tick needs a durable
record. The record is needed right away for the observation week: the timer was
enabled on 2026-11-11 with a systemd drop-in that forces `--dry-run`.

## Decisions

1. **One record per `bridge dispatch` invocation, on every exit path.** That
   covers a config or state read error, paused, outside window, a fetch error,
   the dry-run return, a live apply (with or without error), and a
   `--retry-only` tick. The hook is a thin wrapper around the existing tick
   body, so no exit path can skip it. `dispatch status`, `pause` and `resume`
   are not ticks and write nothing.
2. **Trigger and mode are independent.** `trigger` = `timer` when `--auto` is
   set, otherwise `manual` (`dispatch now`, plain `bridge dispatch`, CLI
   `--dry-run`). `mode` = `dry-run` when `--dry-run` is set, otherwise `live`.
   The observation-week timer (`--auto` + drop-in `--dry-run`) therefore writes
   `trigger: timer, mode: dry-run`, and fills #380's `:00`/`:30` slots. Reading
   the parent design's "manual (`now`, CLI `--dry-run`)" as "any `--dry-run` is
   manual" would mark every observation-week tick as a gap.
3. **Per-decision outcome** (`decision` field):
   - `skipped`: `Dispatch == false`. `reason` is the skip reason.
   - `would_dispatch`: `Dispatch == true` and the tick is `--dry-run` **or**
     the lane is `dry_run`. `decisionStatus` only says WOULD for a dry-run lane.
     The journal also says it for a live lane on a `--dry-run` tick, which is
     the observation-week case.
   - `dispatched`: `Dispatch == true`, live, and the run was **booked** in the
     spend ledger. That is the same "the label landed, the pipeline will act"
     rule `applyDecisions` already uses (a failing follow-up comment still
     books). For a #253 retry, it means `applyRetry` reported `booked`.
   - `not_applied`: `Dispatch == true`, live, but not booked. This happens
     because `applyDecisions` stopped on an earlier error or no GitHub client
     resolved. The tick-level `error` explains it.
4. **Tick outcome** (`outcome` field): `paused`, `outside_window`, `error`
   (`error` carries the text; decisions gathered before the error are kept),
   `no_candidates` (the fetch ran and produced zero decisions), or `decided`.
   A budget-rung block is per-decision (`budget-exhausted …` /
   `budget-unknown` skip reasons). The tick carries the rung reading
   (`budget: {used_usd, limit_usd, known}`) whenever the rung is on, so a
   "nothing dispatched" day can show the number that stopped it.
5. **Context fields:**
   - `at` is the tick's `now`, or entry time if the tick failed before resolving
     it.
   - `lanes_in_window` lists the names of the lanes whose window covers `now`,
     with `default` included when the implicit default lane does. It is
     computed from config alone, without a network call. An out-of-window
     record has an empty list. It is also empty on a paused record (the pause
     gate runs before the schedule is resolved) and when the config could not
     be read.
   - `repos` lists `owner/name` for each repo fetched.
   - `retry_only` is set on `--retry-only` ticks (#253).
   - Each decision has `owner`, `repo`, `issue`, `title`, `kind`
     (`dispatch` | `retry`), `retry_reason`, `lane`, `lane_reason` (the
     downgrade reason), `decision` and `reason`.
6. **Storage:**
   - The journal is daily JSON-lines files under the bridge cache dir:
     `<cacheRoot>/dispatch-journal/YYYY-MM-DD.jsonl`, named by the **UTC** date
     of `at`.
   - Each record is one `json.Marshal` line plus `\n`, written with a single
     `Write` on a file opened `O_APPEND|O_CREATE|O_WRONLY`, `0o644`. Linux
     guarantees append-offset atomicity per `write`, so a concurrent manual
     tick and timer tick cannot interleave inside a line.
   - Readers never lock.
7. **Bounded: 30 days.** After each append, the writer deletes `*.jsonl` files
   in the journal dir whose name parses as a date more than 30 days before
   `at`'s UTC date. Files with other names are left alone. Pruning is per
   file, so no rewrite ever races an append. Retention is a constant
   (`dispatch.JournalRetentionDays = 30`), not a config key.
8. **Never fails the tick.** A journal write or prune error is logged with
   `slog.Warn` and nothing else. The tick's own error, exit code and stdout
   are unchanged. Nothing is ever written to stdout: existing tests parse
   `--json` stdout. The journal never touches `dispatch.json` state.
9. **Reader for #380:** `dispatch.ReadJournal(dir string, since time.Time)
   ([]JournalRecord, error)`.
   - It returns the records with `At >= since`, sorted by `At` (oldest first).
   - A missing dir gives `nil, nil`.
   - It skips a line that does not decode, which covers a partial trailing line
     read mid-write and a corrupt line. It counts skipped lines, but they are
     not an error.
   - A file whose date is before `since`'s UTC date is not opened.
10. **Record version.** Every record has `"v": 1`. #380 ignores records with
    an unknown `v`.

## Coordination with #253

#253 is already dispatched (`ai-implement`, 2026-11-11) and changes the same
functions in `cmd/bridge/dispatch.go`:

- `runDispatch` (the gather lines become `tickCandidates`, and `retrySkips`
  are appended);
- `applyDecisions` (the retry branch calling `applyRetry`);
- the flag vars and `init` (`dispatchRetryOnly`);
- `decisionJSON`;
- `dispatch.Candidate` (`Retry`, `RetryReason`).

**Merge order: #253 first, then #379.** The #379 plan is written against the
post-#253 shape and starts with a precondition check. Because the hook wraps
the whole tick body, retry-pass decisions (selected and skipped) are journaled
with no extra wiring beyond `kind`/`retry_reason`. Do not dispatch #379 while
#253's PR is open.

## Non-goals

- The digest, expected-slot computation, gap detection and the cockpit page
  (#380 and later).
- Per-host or remote journals. Serve and the timer share one host (parent
  design).
- Replacing `journalctl` logging or the `--json` output.
- Forgejo / Azure DevOps (dispatch is GitHub-only today).

## Testing

- `internal/dispatch`, all `t.TempDir()`:
  - the append/read round trip;
  - UTC day-file naming across midnight;
  - prune boundaries (31 days deleted, 30 kept, foreign file names kept);
  - a malformed trailing line is skipped;
  - the `since` filter, plus not opening older files;
  - a missing dir;
  - 20 concurrent appends under `-race` all read back.
- `cmd/bridge` pure builders (table tests):
  - `tickTrigger`/`tickMode`;
  - `lanesInWindow`;
  - `journalDecisions`, the classification table above including tick-dry-run
    with a live lane, and a retry;
  - `markBooked`;
  - `finishRecord` (outcome derivation).
- `cmd/bridge` integration (httptest fake GitHub, existing pattern):
  - `--auto --dry-run` gives `timer`/`dry-run`/`would_dispatch`;
  - a manual live tick gives `manual`/`live`/`dispatched`;
  - a label failure gives `not_applied` + outcome `error`;
  - paused and outside-window records with no fetch;
  - a `--retry-only --dry-run` record;
  - an unwritable journal dir still gives a successful tick;
  - a config read error gives an `error` record.
  - Existing tests stay green unmodified.

## Docs

- `docs/dispatch.md`: a "Dispatch journal" section covering the path, record
  shape, outcomes, retention and the fact that it is safe to read while ticks
  run. Also a pointer in "Systemd timer and service" (dry-run observation ticks
  are journaled as `timer`).
- CHANGELOG `Added`.
