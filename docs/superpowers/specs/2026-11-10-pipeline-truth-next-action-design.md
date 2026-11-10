# NextAction reads the pipeline's truth — design (#367)

## Why

bridge and the agent-workflow pipeline have two retry/attempt models and they
disagree. For the issue-centric inbox (#368) and the unattended retry ticks (#253)
there must be exactly one answer to "how often was this issue tried, and why did it
stop". The pipeline is that answer; bridge reads it and only decides *whether a
human or a re-dispatch is needed*.

What is wrong today (verified on `main` 3aeaa62 and agent-workflow `main`):

- `internal/dispatch/failure.go` keys on `failed:<bucket>` and `attempt:N` labels.
  **The pipeline never sets either.** No script or workflow in agent-workflow
  writes them, and no open issue under `freaxnx01` carries one (2026-11-10).
  `docs/dispatch.md` ("Labels", "When a run fails") claims the pipeline sets them.
- Its buckets (`api_auth`, `rate_limit`, `infra`, `max_turns`, `gate_failed`,
  `no_diff`) are not the pipeline's classes. The pipeline's classes
  (`scripts/classify-failure.sh`) are: `success`, `transient`, `rate_limit`,
  `task_failure`, `api_auth`, `model_not_found`, `bug`, and the pause class
  `out_of_credits` (`scripts/lib/pause-reasons.sh`). `api_auth` is transient for
  bridge but "no retry, operator ping" for the pipeline.
- **Parked-label mismatch (bug, in scope by user decision).** bridge recognises
  only `🧊 parked` (`internal/dispatch/eligible.go:15,107`,
  `internal/cockpit/board.go:149,269`). The pipeline parks with ASCII `parked`
  (ADR-016; `check-attempt-cap.sh` adds `parked` and removes `ai-implement`). A
  pipeline-parked issue therefore looks eligible to bridge: bridge re-applies
  `ai-implement`, the pipeline's cap step parks it again, and every tick leaves
  another `## ai-implement parked` comment.
- `Eligible` also parks at `attempt:N >= 2` — a second counter that can disagree
  with the pipeline's cap.
- `NextAction` has no caller. Wiring it to the timer is #253, not this issue.

## Goal

1. bridge reads the latest run's facts from the pipeline's run marker
   (freaxnx01/agent-workflow#585, ADR-021 there) and nothing else.
2. `NextAction` maps pipeline state onto four outcomes — none, wait, retry, human —
   with one table, documented in `docs/dispatch.md`.
3. Both parked spellings are recognised; bridge itself only ever writes `parked`.
4. The `failed:*` / `attempt:N` model is removed from code and docs.

## Non-goals

- Calling `NextAction` from a tick or re-dispatching anything (#253).
- Any UI (#368). The cockpit only gets the parked fix.
- Changing the pipeline.
- A fallback parser for the visible run report. See *No marker*.

## The run record

New file `internal/dispatch/runrecord.go`.

```go
// RunRecord is one pipeline run, decoded from the run marker the pipeline
// appends to its run report (agent-workflow ADR-021).
type RunRecord struct {
    Attempt      int     // lifetime attempt number; 0 = absent (run did not count, or cap step skipped)
    Counts       bool
    RetryAttempt int
    Class        string  // classify-failure.sh class; "" = absent
    Decision     string  // "retry" | "stop" | ""
    Status       string  // "ai:done" | "ai:failed" | "ai:partial" | ""
    Subtype      string
    Agent, Model string
    Turns        int
    CostUSD      float64
    PR           int
    RunURL       string
    Posted       time.Time // Created of the comment the marker came from
}
```

- `ParseRunMarker(body string) (RunRecord, bool)` — reads the **last non-empty
  line** of the comment body only; it must start with `<!-- ai-run: ` and end with
  ` -->`; the JSON between them must decode, and `v` must be `1`. Anything else →
  `false`. Unknown fields are ignored (additive fields are not a version bump).
- `LatestRun(comments []forge.Comment) (RunRecord, bool)` — considers only
  comments with `Author == "github-actions[bot]"` (the identity the pipeline's
  `Post run report` step posts as), and returns the record from the most recent
  one (by `Created`; ties → later in slice order) that parses. The author is a
  constant `pipelineReportAuthor` next to the parser, with a comment pointing at
  ADR-021's trust rule.

These rules are agent-workflow ADR-021's consumer trust rule. Text from any other
author — including a comment that copies a marker — is display-only (ADR-019).

## NextAction

```go
type ActionKind int
const (
    ActionNone  ActionKind = iota // nothing to do (no failure, or done)
    ActionWait                    // the pipeline is still on it, or a resume will re-dispatch it
    ActionRetry                   // the pipeline stopped for a reason a later re-dispatch can fix
    ActionHuman                   // a person has to look
)

type Action struct {
    Kind   ActionKind
    Reason string // short, stable, lower-case; shown in status output and #368 cards
}

// IssueState is everything NextAction looks at. The caller fills it; computing
// InFlight (a workflow run for this issue that is queued or in progress) is the
// caller's job — #253 and #368 already list workflow runs (internal/cockpit).
type IssueState struct {
    Labels   []string
    Run      RunRecord
    HasRun   bool      // LatestRun found a marker
    InFlight bool
    Now      time.Time
}

func NextAction(s IssueState) Action
```

`staleRetryAfter = 3 * time.Hour` — the longest a `decision: retry` may go without
a newer report before a person is asked. The pipeline's longest retry delay is
5 minutes plus one run (well under an hour), so 3 h means the retry was lost (a
403 on re-dispatch, or a run that timed out and posted no report, agent-workflow
#567). *(My call.)*

It is pure: no I/O, no label writes, no comments. The old `Action` fields
(`AddLabels`, `RemoveLabels`, `Comment`, `Retry`) go away with the label model.

### Mapping (first matching row wins)

| # | Pipeline state | Kind | Reason |
|---|---|---|---|
| 1 | label `parked` or `🧊 parked` | Human | `parked` |
| 2 | label `ai:paused` | Wait | `paused` |
| 3 | `InFlight` | Wait | `running` |
| 4 | label `ai:review-blocked` | Human | `review blocked` |
| 5 | label `ai:review-failed` | Human | `review failed` |
| 6 | label `ai:runner-blocked` | Human | `runner blocked` |
| 7 | label `ai:checks-blocked` | Human | `checks blocked` |
| 8 | no `ai-implement` and none of `ai:failed` / `ai:partial` / `ai:done` | None | `` |
| 9 | `ai-implement`, `HasRun == false`, no status label | Wait | `no run report yet` |
| 10 | `HasRun == false`, any status label | `ai:done` → None; `ai:failed` / `ai:partial` → Human | `no run marker` |
| 11 | `Run.Status == "ai:done"` | None | `` |
| 12 | `Run.Status == "ai:partial"` | Human | `partial` |
| 13 | class `out_of_credits` (pause class) | Wait | `paused` |
| 14 | class `transient`, `rate_limit` or `task_failure`, decision `retry`, `Now - Run.Posted > staleRetryAfter` | Human | `retry never reported` |
| 15 | class `transient`, `rate_limit` or `task_failure`, decision `retry` | Wait | `pipeline retrying` |
| 16 | class `transient` or `rate_limit`, decision `stop`, `Run.Attempt > 0` | Retry | `<class>, retries exhausted` |
| 17 | class `transient` or `rate_limit`, decision `stop`, `Run.Attempt == 0` | Human | `<class>, no attempt cap` |
| 18 | class `task_failure`, decision `stop` | Human | `task_failure` |
| 19 | class `api_auth`, `model_not_found`, `bug` | Human | `<class>` |
| 20 | anything else (unknown class, empty decision on a failure) | Human | `unknown: <class>` |

Label names are exactly those `scripts/ensure-issue-labels.sh` creates in
agent-workflow. `ai:running` is deliberately absent: the pipeline creates and
removes it but never sets it, so "running" is row 3 (`InFlight`) or, before the first report, row 9. Labels alone
cannot tell a re-dispatched run from the last finished one: `ai-implement` and the
previous status label and marker all stay until the next report. That is why
`InFlight` is an input. *(My call.)*

Notes:

- Row 16 is the only `Retry`. A re-dispatch there is bounded by the pipeline's own
  attempt cap, which is why it requires `Run.Attempt > 0` (absent when the cap is
  off: `max-attempts: 0`, stub runs, pause gate; row 17 sends those to a person): transient and rate-limit runs that reached the agent count as
  attempts, so the cap parks the issue at its limit and row 1 takes over.
- Row 18 is `Human`, not `Retry`: the pipeline already retried a turn-budget
  failure once; a third try is what the cap exists to prevent.
- Row 9 vs 10: an issue that was just labelled has no report yet (wait); a failed
  issue whose report predates the marker (#585) or was posted by someone else
  goes to a human. **Unknown is never Retry** — that is the fail-safe direction
  ADR-019 asks for.
- Rows 2/13 both say paused: the label is set by the pipeline when it pauses
  (ADR-018); `pipeline-resume.sh` re-dispatches those issues itself.
- `agent_timeout` (named in #368) is not a pipeline class. A timed-out run posts
  no report (agent-workflow #567) and lands in row 9, 10 or 14.

## Parked

- `eligible.go`: `LabelParked` becomes `"parked"`; add
  `LabelParkedLegacy = "🧊 parked"` and `IsParked(labels []string) bool` (either
  spelling). `Eligible` and `internal/cockpit/board.go` (both call sites) use
  `IsParked`.
- After this change no bridge code writes `🧊 parked`.
- One-off migration — a manual operator step listed in the plan, not code: the
  three open issues carrying `🧊 parked` on 2026-11-10 (freaxnx01/bridge#87,
  freaxnx01/bridge#197, freaxnx01/game-tschau-sepp#36) are relabelled `parked`;
  the `🧊 parked` label definition is then deleted in each repo that has it. The
  legacy spelling stays recognised in code, so the order does not matter.

## Removed

- `Attempts`, `FailureBucket`, `IsTransient`, `transientBuckets`, `maxAttempts`,
  `attemptPrefix`, `failedPrefix`, and the `attempt:N >= 2` rule in `Eligible`
  (the pipeline's cap parks; row 1 covers it). Their tests are deleted with them —
  the behaviour is removed by decision, not changed to make a test pass.
- `docs/dispatch.md`: the `failed:*` and `attempt:N` rows of the label table, the
  "Transient / Substantive failures" subsections, and "Not parked" wording that
  names only `🧊 parked`. Replaced by the mapping table above under
  "When a run fails", and a label table listing `parked` (legacy `🧊 parked`
  still read), `ai:paused`, `ai:review-blocked`, `ai:review-failed`, `ai:runner-blocked`, `ai:checks-blocked`, `ai:done` / `ai:failed` / `ai:partial`
  as set by the pipeline. `docs/cockpit.md:27` names both spellings.

## No marker → no fallback parser

The issue asked for a fallback to the visible report until #585 ships. #585 is
dispatched and this issue is dispatched after it merges, so a fallback would only
serve reports older than the marker. Those carry no class and no retry decision
(the visible outcome is `failed: <subtype>`), so a fallback could not fill the
table's inputs anyway. Rows 9/10 handle "no marker" safely. *(My call.)*

## Forges

GitHub only in v1. The marker's trusted author is `github-actions[bot]`, Azure
DevOps emits no marker (agent-workflow ADR-021), and Forgejo has no pipeline. On
those forges `HasRun` is always false, so failures land in row 10 (Human).
*(My call.)*

## Dependencies and order

- Depends on agent-workflow#585 being merged (marker format, ADR-021). The format
  is fixed by `docs/superpowers/specs/2026-11-10-run-marker-design.md` in
  agent-workflow; this design follows it, so planning does not wait.
- #253 (`--retry-only`) calls `NextAction` and acts on `ActionRetry`; enriched
  after this merges.
- #368 shows `ActionHuman` as cards and `Wait` as one-line status.

## Consequences

- Until #585's runs accumulate, every currently failed issue reads as row 10
  (`no run marker` → Human). That is a visible but safe default.
- `bridge dispatch` stops re-dispatching pipeline-parked issues immediately, which
  ends the parked-comment loop described above.
- Removing the `attempt:N >= 2` rule changes no live behaviour: no issue carries
  the label.
