# Per-Tick Dispatch Journal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every `bridge dispatch` invocation appends exactly one JSON-lines record to a bounded, append-only journal under the bridge cache dir. The record holds trigger, mode, lanes in window, repos, the budget reading and every decision with its real outcome. `bridge serve` (#380) can read it without locking the tick.

**Architecture:** Package `dispatch` gains `journal.go`, which holds the record types, `AppendJournal` (one `O_APPEND` write into a UTC day file, then a per-file prune past 30 days) and `ReadJournal` (tolerant reader for #380). `cmd/bridge` gains `dispatch_journal.go`, which holds pure builders (trigger, mode, lanes in window, decision classification, outcome) and `recordTick`. That function writes the record and only logs on failure. `runDispatch` becomes a wrapper: it builds the record, calls the old body (renamed `runTick`, which fills the record at each exit point), then calls `recordTick`. `applyDecisions` keeps its signature as a wrapper over a new `applyDecisionsBooked`, which also returns the booked runs, so `dispatched` means "booked" exactly.

**Tech Stack:** Go (as pinned in `go.mod`), Cobra, stdlib `testing` (table-driven `t.Run`, hand-rolled fakes), `net/http/httptest`, `encoding/json`, `log/slog`.

**Spec:** `docs/superpowers/specs/2026-11-11-dispatch-journal-design.md`

## Global Constraints

- **Merge order: #253 (`--retry-only`) merges first, then this plan.** #253 is already dispatched. It changes `runDispatch` (adds `tickCandidates`, `retrySkips`), `applyDecisions` (adds the `d.Candidate.Retry` → `applyRetry` branch), the flag vars and `init` (`dispatchRetryOnly`), `decisionJSON`, and `dispatch.Candidate` (`Retry`, `RetryReason`). This plan is written against that post-#253 shape. **Task 0 is a hard precondition:** if any #253 symbol is missing on `main`, stop and report. Do not re-implement #253, and do not change any #253 behaviour.
- Go: stdlib `testing` only, table-driven, hand-rolled fakes / `httptest`. **No** testify/mockery/gomock. **No new Go modules.** Test names follow `TestFunc_State_Expected`.
- No package-level mutable state. `ctx` comes first where I/O happens. Errors are wrapped with `%w`, lower-case.
- **Journaling never changes a tick's result.** A write or prune failure is a `slog.Warn` only. `runDispatch` returns exactly the error the tick produced, stdout is byte-identical to today, and `dispatch.json` state is untouched by the journal.
- One record per invocation of `runDispatch`, on **every** exit path. `dispatch status`/`pause`/`resume` write nothing.
- `trigger` = `"timer"` iff `--auto`, else `"manual"`. `mode` = `"dry-run"` iff `--dry-run`, else `"live"`. The two are independent.
- Decision classification:
  - `!Dispatch` → `skipped`.
  - Tick dry-run **or** `Lane.DryRun` → `would_dispatch`.
  - Live and booked in the ledger → `dispatched`.
  - Live and not booked → `not_applied`.
- Tick outcome:
  - Preset `paused` / `outside_window` is kept.
  - Otherwise, an error → `error` (the decisions are kept).
  - Otherwise, zero decisions after the fetch → `no_candidates`.
  - Otherwise → `decided`.
- Storage:
  - Path: `<cacheRoot>/dispatch-journal/YYYY-MM-DD.jsonl`, named by the UTC date of `at`.
  - One `Write` per record, on a file opened `O_APPEND|O_CREATE|O_WRONLY`, `0o644`.
  - Prune after each append: delete `YYYY-MM-DD.jsonl` files dated more than 30 days before `at`'s UTC date. Other names are kept.
  - `JournalRetentionDays = 30` is a const, not config.
- Existing tests stay **unmodified** and green, notably:
  - `TestRunDispatch_JSONLiveTick_CallsApplyDecisions` (parses stdout);
  - `TestRunDispatch_DryRunJSON_SkipsApplyDecisions` (LastTick unset);
  - `TestRunDispatchAutoOutsideWindowSkipsBeforeFetching` (no fetch);
  - the `applyDecisions(...)` caller at `cmd/bridge/dispatch_test.go:988`.
- New `cmd/bridge` tests that run a tick set `XDG_CACHE_HOME` to a temp dir (the journal lands in it) and call `pinUnattendedSchedule(t)` where a window matters.
- Gates after every task, all green before commit:
  - `gofmt -l .` empty;
  - `go vet ./...`;
  - `golangci-lint run`;
  - `go test -race ./...`.

  While iterating, use `just test-fast` or `go test ./cmd/bridge -run '<pattern>'`. `cmd/bridge` takes about 150 s.
- Conventional Commits that reference `#379`. Scopes: `dispatch`, `docs`.

## Review Focus

1. **Observation-week tick** (`--auto --dry-run`): the record says `trigger: timer, mode: dry-run`, and a live-lane candidate is `would_dispatch`, not `dispatched`. Tested in Task 2 (table) and Task 3 (integration).
2. **`dispatched` is never claimed for an unlabelled issue.** A label POST failure leaves that decision `not_applied` and makes the outcome `error`. A failing comment *after* the label still gives `dispatched`, matching the ledger. Tested in Task 3.
3. **Early exits** (paused, outside window, config error) still write one record, with no network call. Tested in Task 3.
4. **The journal dir is unwritable** (the path is a regular file): the tick returns nil and its output is unchanged. Tested in Task 3.
5. **The reader mid-write:** a partial trailing line is skipped, not an error. Tested in Task 1.

---

### Task 0: Precondition — #253 is on `main`

- [ ] **Step 1:** Run
  ```bash
  git fetch origin && git log --oneline origin/main | grep -E '#253' | head
  grep -n 'Retry  *bool' internal/dispatch/order.go
  grep -n 'func tickCandidates' cmd/bridge/dispatch_retry.go
  grep -n 'dispatchRetryOnly' cmd/bridge/dispatch.go
  grep -n 'func applyRetry' cmd/bridge/dispatch_retry.go
  ```
  Every command must print at least one line. If any prints nothing, **stop**: comment on #379 "blocked: #253 not merged" and end the run with no code changes.
- [ ] **Step 2:** Read the current `runDispatch` and `applyDecisions` in `cmd/bridge/dispatch.go` in full. Task 3 inserts into them, and their exact post-#253 lines are the anchor. Their structure should be: config → state → pause gate → `now`/`timing` → window gate → fetch → `tickCandidates` → partition → budget → `ApplyCaps` → `mergeInOrder` + `retrySkips` → render → dry-run return → `applyDecisions`. If the order differs, adapt the insertion points without changing behaviour.

### Task 1: `dispatch` journal store

**Files:**
- Create: `internal/dispatch/journal.go`, `internal/dispatch/journal_test.go`

**Interfaces (produces, package `dispatch`):**

```go
const (
	JournalVersion       = 1
	JournalRetentionDays = 30

	TriggerTimer  = "timer"
	TriggerManual = "manual"
	ModeLive      = "live"
	ModeDryRun    = "dry-run"

	OutcomeDecided       = "decided"
	OutcomeNoCandidates  = "no_candidates"
	OutcomePaused        = "paused"
	OutcomeOutsideWindow = "outside_window"
	OutcomeError         = "error"

	DecisionDispatched    = "dispatched"
	DecisionWouldDispatch = "would_dispatch"
	DecisionSkipped       = "skipped"
	DecisionNotApplied    = "not_applied"

	KindDispatch = "dispatch"
	KindRetry    = "retry"
)

// JournalRecord is one dispatch tick. The json shape is a contract with the
// cockpit digest (#380): add fields, never rename or repurpose one.
type JournalRecord struct {
	V             int               `json:"v"`
	At            time.Time         `json:"at"`
	Trigger       string            `json:"trigger"`
	Mode          string            `json:"mode"`
	RetryOnly     bool              `json:"retry_only,omitempty"`
	Outcome       string            `json:"outcome"`
	Error         string            `json:"error,omitempty"`
	LanesInWindow []string          `json:"lanes_in_window"`
	Repos         []string          `json:"repos,omitempty"`
	Budget        *JournalBudget    `json:"budget,omitempty"`
	Decisions     []JournalDecision `json:"decisions,omitempty"`
}

// JournalBudget is the budget rung's reading, present only when the rung is on.
type JournalBudget struct {
	UsedUSD  float64 `json:"used_usd"`
	LimitUSD float64 `json:"limit_usd"`
	Known    bool    `json:"known"`
}

type JournalDecision struct {
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Issue       int    `json:"issue"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	RetryReason string `json:"retry_reason,omitempty"`
	Lane        string `json:"lane"`
	LaneReason  string `json:"lane_reason,omitempty"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason,omitempty"`
}

// AppendJournal writes rec as one line to dir's day file for rec.At (UTC) and
// prunes day files past the retention. rec.V is forced to JournalVersion.
func AppendJournal(dir string, rec JournalRecord) error

// ReadJournal returns the records at or after since, oldest first. A missing
// dir is no records. Lines that do not decode (a partial line read mid-write)
// are skipped.
func ReadJournal(dir string, since time.Time) ([]JournalRecord, error)
```

Implementation notes:
- `dayFile(dir, at) = filepath.Join(dir, at.UTC().Format("2006-01-02")+".jsonl")`.
- In `AppendJournal`:
  1. Call `os.MkdirAll(dir, 0o755)`.
  2. Build `line := append(json.Marshal(rec), '\n')`.
  3. Run `f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)`, then a single `f.Write(line)`.
  4. Join the `Close` error with `errors.Join`.
  5. Call `pruneJournal(dir, rec.At)`.
- In `pruneJournal(dir, at)`:
  1. Compute `cutoff := at.UTC().Truncate(24*time.Hour).AddDate(0, 0, -JournalRetentionDays)`.
  2. For each entry, `time.Parse("2006-01-02", strings.TrimSuffix(name, ".jsonl"))`. A non-`.jsonl` name or a parse error → keep the file.
  3. If `day.Before(cutoff)` → `os.Remove`.
  4. Return the joined errors.
- In `ReadJournal`:
  1. Run `os.ReadDir`. `fs.ErrNotExist` → `nil, nil`.
  2. Sort the names. Skip files dated before `since`'s UTC day.
  3. Read each file with `bufio.Scanner` (buffer 1 MiB).
  4. `json.Unmarshal` each line. On error, `continue`.
  5. Keep the records with `!r.At.Before(since)`, then `sort.SliceStable` by `At` (oldest first).
- Each function stays at 20 lines or fewer, with helpers extracted.

- [ ] **Step 1: Write the failing tests** (`journal_test.go`, package `dispatch`):

```go
func rec(at time.Time, outcome string) JournalRecord {
	return JournalRecord{At: at, Trigger: TriggerTimer, Mode: ModeDryRun, Outcome: outcome, LanesInWindow: []string{"default"}}
}

func TestAppendJournal_RoundTrip_ReadsBackWithVersion(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	r := rec(at, OutcomeDecided)
	r.Decisions = []JournalDecision{{Owner: "freaxnx01", Repo: "bridge", Issue: 41, Title: "t", Kind: KindDispatch, Lane: "default", Decision: DecisionWouldDispatch}}
	if err := AppendJournal(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJournal(dir, at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].V != JournalVersion || got[0].Decisions[0].Issue != 41 || !got[0].At.Equal(at) {
		t.Fatalf("got %+v", got)
	}
}

func TestAppendJournal_DayFile_NamedByUTCDate(t *testing.T) {
	dir := t.TempDir()
	zurich := time.FixedZone("CET", 3600)
	at := time.Date(2026, 11, 12, 0, 30, 0, 0, zurich) // 2026-11-11T23:30Z
	if err := AppendJournal(dir, rec(at, OutcomePaused)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-11.jsonl")); err != nil {
		t.Fatalf("want UTC day file: %v", err)
	}
}

func TestAppendJournal_Prune_RemovesOnlyFilesPastRetention(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2026-10-11.jsonl", "2026-10-12.jsonl", "notes.txt", "garbage.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) // cutoff day 2026-10-12
	if err := AppendJournal(dir, rec(at, OutcomeDecided)); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"2026-10-11.jsonl": false, "2026-10-12.jsonl": true, "notes.txt": true,
		"garbage.jsonl": true, "2026-11-11.jsonl": true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if exists := err == nil; exists != want {
			t.Errorf("%s exists=%t, want %t", name, exists, want)
		}
	}
}

func TestReadJournal_PartialTrailingLine_Skipped(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	if err := AppendJournal(dir, rec(at, OutcomeDecided)); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "2026-11-11.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if _, err := f.WriteString(`{"v":1,"at":"2026-11-11T02:30`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJournal(dir, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d records, err %v", len(got), err)
	}
}

func TestReadJournal_Since_FiltersRecordsAndOlderFiles(t *testing.T) {
	dir := t.TempDir()
	d1 := time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 11, 11, 0, 30, 0, 0, time.UTC)
	for _, at := range []time.Time{d3, d1, d2} {
		if err := AppendJournal(dir, rec(at, OutcomeDecided)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadJournal(dir, d2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].At.Equal(d2) || !got[1].At.Equal(d3) {
		t.Fatalf("want [d2 d3] sorted by At, got %+v", got)
	}
	all, _ := ReadJournal(dir, time.Time{})
	if len(all) != 3 || !all[0].At.Equal(d1) {
		t.Fatalf("want 3 records, oldest (d1) first across files, got %+v", all)
	}
}

func TestReadJournal_MissingDir_NoRecordsNoError(t *testing.T) {
	got, err := ReadJournal(filepath.Join(t.TempDir(), "absent"), time.Time{})
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestAppendJournal_Concurrent_AllRecordsIntact(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := AppendJournal(dir, rec(at.Add(time.Duration(i)*time.Second), OutcomeDecided)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := ReadJournal(dir, time.Time{})
	if err != nil || len(got) != 20 {
		t.Fatalf("got %d records, err %v", len(got), err)
	}
}
```

  Note: `d3` is appended before `d2` into the same day file on purpose. A manual tick and a timer tick can finish out of order, so `ReadJournal` sorts by `At` (`sort.SliceStable`) rather than trusting append order.
- [ ] **Step 2: Run to verify failure:** `go test ./internal/dispatch -run 'Journal' -v` → FAIL (undefined).
- [ ] **Step 3: Implement** `journal.go` as specified.
- [ ] **Step 4: Run the gates.** Expected: green.
- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/journal.go internal/dispatch/journal_test.go
git commit -m "feat(dispatch): add an append-only per-tick journal store (#379)"
```

### Task 2: Pure record builders in `cmd/bridge`

**Files:**
- Create: `cmd/bridge/dispatch_journal.go`, `cmd/bridge/dispatch_journal_test.go`

**Interfaces (produces, package `main`):**

```go
func dispatchJournalDir() string { return filepath.Join(cacheRoot(), "dispatch-journal") }

func tickTrigger(auto bool) string   // TriggerTimer iff auto
func tickMode(dryRun bool) string    // ModeDryRun iff dryRun

// newTickRecord starts a record at entry; runTick overwrites At with the
// tick's resolved now.
func newTickRecord(at time.Time, auto, dryRun, retryOnly bool) dispatch.JournalRecord

// lanesInWindow names the lanes acting at now, "default" first when the
// implicit default lane acts. Config only — no network.
func lanesInWindow(cfg dispatch.Config, now time.Time) []string

func repoNames(repos []repoInput) []string // "owner/name", fetch order

func journalBudget(b dispatch.BudgetState) *dispatch.JournalBudget // {UsedUSD, LimitUSD, Known: !b.Unknown}

// journalDecisions classifies each decision; live Dispatch=true starts as
// not_applied until markBooked confirms it.
func journalDecisions(ds []dispatch.Decision, tickDryRun bool) []dispatch.JournalDecision

// markBooked flips not_applied → dispatched for each booked run (Repo+Issue).
func markBooked(js []dispatch.JournalDecision, runs []usage.Run)

// finishRecord sets Outcome/Error from the tick's error and decisions.
func finishRecord(rec *dispatch.JournalRecord, tickErr error)

// recordTick finishes rec and appends it; a failure is a slog.Warn, never returned.
func recordTick(dir string, rec dispatch.JournalRecord, tickErr error)
```

`journalDecisions` maps each decision as follows:
- `Owner`, `Repo` and `Title` come from `Candidate`, and `Issue` from `Candidate.Issue.Number`.
- `Kind` is `KindRetry` if `Candidate.Retry`, else `KindDispatch`. `RetryReason` comes from `Candidate.RetryReason`.
- `Lane` is `Candidate.Lane.Name`, `LaneReason` is `Candidate.LaneReason`, and `Reason` is `d.Reason`.
- `Decision` follows the classification in Global Constraints.

`finishRecord`:
1. If `tickErr != nil`, set `rec.Error = tickErr.Error()`, and set `Outcome = OutcomeError` unless `Outcome` is already `paused`/`outside_window`.
2. If `Outcome` is empty, set `OutcomeNoCandidates` when `len(Decisions) == 0`, else `OutcomeDecided`.

- [ ] **Step 1: Write the failing tests:**
  - `TestTickTriggerMode_Table`:
    - `(auto=true, dry=true)` → `timer`/`dry-run` (the observation-week drop-in);
    - `(true, false)` → `timer`/`live`;
    - `(false, true)` → `manual`/`dry-run`;
    - `(false, false)` → `manual`/`live`.
  - `TestLanesInWindow`:
    - The config has lane `auto` with windows `[{00:00 00:00}]` and lane `night` with `[{22:00 23:00}]`.
    - The top-level schedule comes from `pinUnattendedSchedule`'s body, parsed via `dispatch.LoadConfig` from a temp file, or the equivalent struct literal.
    - At 13:00 → `["default", "auto"]`.
    - With the top-level schedule `{from: 03:00, to: 03:01}` at 13:00 → `["auto"]`.
  - `TestJournalDecisions_Classification` (table). Base candidate: `Owner freaxnx01`, `Repo bridge`, `Issue{Number: 41, Title: "t"}`, `Lane{Name: "default"}`.
    - `Dispatch=false, Reason "global cap 3/3"`, live → `skipped`, `reason "global cap 3/3"`.
    - `Dispatch=true`, tick dry-run, live lane → `would_dispatch`.
    - `Dispatch=true`, live tick, `Lane.DryRun=true` → `would_dispatch`.
    - `Dispatch=true`, live tick, live lane → `not_applied`.
    - `LaneReason "auto→hitl: x"` → copied to `lane_reason`.
    - `Candidate{Retry: true, RetryReason: "rate_limit, retries exhausted"}`, `Dispatch=true`, tick dry-run → `kind retry`, `retry_reason` set, `would_dispatch`.
    - Plain → `kind dispatch`.
  - `TestMarkBooked_FlipsOnlyBookedLive`: three decisions: `#41` not_applied, `#42` not_applied, `#43` would_dispatch. Runs `[{Repo:"bridge", Issue:41}, {Repo:"bridge", Issue:43}]` → `#41 dispatched`, `#42 not_applied`, `#43 would_dispatch` (unchanged).
  - `TestFinishRecord_Outcome` (table):
    - nil error, no decisions → `no_candidates`;
    - nil error, one decision → `decided`;
    - error `boom` with decisions → `error`, `Error "boom"`, decisions kept;
    - preset `paused`, nil → `paused`;
    - preset `outside_window`, nil → `outside_window`.
  - `TestRecordTick_UnwritableDir_NoPanicNoReturn`: `dir` is a path to a regular file. `recordTick` returns, there is nothing to assert beyond no panic, and `ReadJournal` on a temp dir sibling still works. The point is that `recordTick` has no error return.
- [ ] **Step 2: Run to verify failure:** `go test ./cmd/bridge -run 'TickTrigger|LanesInWindow|JournalDecisions|MarkBooked|FinishRecord|RecordTick' -v` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the gates.**
- [ ] **Step 5: Commit**
```bash
git add cmd/bridge/dispatch_journal.go cmd/bridge/dispatch_journal_test.go
git commit -m "feat(dispatch): build journal records from tick decisions (#379)"
```

### Task 3: Hook the journal onto the shared tick path

**Files:**
- Modify: `cmd/bridge/dispatch.go` (`runDispatch`, `applyDecisions`)
- Modify: `cmd/bridge/dispatch_journal_test.go`

**Changes:**
1. Rename the existing `runDispatch(cmd *cobra.Command, _ []string) error` body to `runTick(cmd *cobra.Command, rec *dispatch.JournalRecord) error`. Add the new wrapper:
   ```go
   // runDispatch runs one tick and journals it. Every exit path of runTick
   // lands in exactly one record; journaling never changes the tick's result.
   func runDispatch(cmd *cobra.Command, _ []string) error {
   	rec := newTickRecord(time.Now(), dispatchAuto, dispatchDryRun, dispatchRetryOnly)
   	err := runTick(cmd, &rec)
   	recordTick(dispatchJournalDir(), rec, err)
   	return err
   }
   ```
   `dispatchNowCmd` already calls `runDispatch`, so it is unchanged.
2. Inside `runTick`, insert these lines and change no other behaviour:
   - In the pause gate, before `return nil`: `rec.Outcome = dispatch.OutcomePaused`.
   - Right after `timing := resolveTiming(cfg, now)`: `rec.At = now` and `rec.LanesInWindow = lanesInWindow(cfg, now)`.
   - In the window gate, before `return nil`: `rec.Outcome = dispatch.OutcomeOutsideWindow`.
   - After a successful `fetchRepoInputs`: `rec.Repos = repoNames(repos)`.
   - After `budget := dispatch.NewBudgetState(...)`: `if timing.RungOn { rec.Budget = journalBudget(budget) }`.
   - After the final decisions slice is complete (after `mergeInOrder` and #253's `append(decisions, retrySkips...)`): `rec.Decisions = journalDecisions(decisions, dispatchDryRun)`.
   - Replace the final `return applyDecisions(ctx, decisions, state, timing, cfg)` with:
     ```go
     runs, err := applyDecisionsBooked(ctx, decisions, state, timing, cfg)
     markBooked(rec.Decisions, runs)
     return err
     ```
3. Rename the existing `applyDecisions` body to `applyDecisionsBooked(...) ([]usage.Run, error)`. It returns the `runs` slice it already builds, including #253's retry-branch bookings, alongside the joined error. Keep a wrapper so the existing test call site compiles unmodified:
   ```go
   func applyDecisions(ctx context.Context, ds []dispatch.Decision, state dispatch.State, timing tickTiming, cfg dispatch.Config) error {
   	_, err := applyDecisionsBooked(ctx, ds, state, timing, cfg)
   	return err
   }
   ```
   Discarding the runs slice there is fine because it is not an error.

- [ ] **Step 1: Write the failing integration tests** (`dispatch_journal_test.go`). Reuse `writeFakeGithubRepo`, `pinUnattendedSchedule` and `setDispatchFlags`, plus a local `newJournalGithub(t, labelStatus int)` httptest server. It serves `GET …/issues` with eligible `#41` (`feat`, body with `## Implementation Plan`), `[]` for milestones and pulls, `POST …/41/labels` (answering `labelStatus`, 200 → `[{"name":"ai-implement"}]`) and `POST …/41/comments` (201). Anything else → `t.Errorf`. Add a helper `readOnlyRecord(t) dispatch.JournalRecord` that calls `ReadJournal(dispatchJournalDir(), time.Time{})` and fails unless there is exactly one record. Each test sets `XDG_CACHE_HOME`, `XDG_CONFIG_HOME`, `BRIDGE_CLAUDE_PROJECTS` (absent) to temp paths, sets `BRIDGE_REPOS_ROOT`/`BRIDGE_GITHUB_API`/`GH_TOKEN` where it fetches, and resets `dispatchAuto`/`dispatchRetryOnly` in `t.Cleanup`.
  - `TestRunDispatch_AutoDryRun_JournalsTimerDryRunWouldDispatch`:
    - Setup: `pinUnattendedSchedule`, `dispatchAuto = true`, `setDispatchFlags(t, false, true)`.
    - Expected: one record with `Trigger "timer"`, `Mode "dry-run"`, `Outcome "decided"`, `Repos ["freaxnx01/bridge"]`, `LanesInWindow` containing `"default"`, and one decision `#41` with `Decision "would_dispatch"`, `Kind "dispatch"`.
    - No POST is made.
  - `TestRunDispatch_ManualLive_JournalsDispatched`:
    - Setup: `pinUnattendedSchedule`, live, label 200.
    - Expected: `Trigger "manual"`, `Mode "live"`, `#41 "dispatched"`, `Outcome "decided"`.
  - `TestRunDispatch_LabelFails_JournalsNotAppliedAndError`:
    - Setup: label 500.
    - Expected: `runDispatch` returns an error; the record has `Outcome "error"`, a non-empty `Error`, and `#41 "not_applied"`.
  - `TestRunDispatch_CommentFailsAfterLabel_JournalsDispatched`:
    - Setup: label 200, comment 500 (a variant of the helper with a `commentStatus` parameter is fine).
    - Expected: `#41 "dispatched"` and `Outcome "error"`. This mirrors `TestRunDispatchBooksTheRunWhenTheCommentFails`.
  - `TestRunDispatch_AutoPaused_JournalsPausedWithoutFetch`:
    - Setup: write state `{Paused: true}` via `dispatch.WriteState(dispatchStatePath(), …)`, `dispatchAuto = true`, no GitHub env.
    - Expected: `Outcome "paused"`, `Trigger "timer"`, `Repos` empty.
  - `TestRunDispatch_AutoOutsideWindow_JournalsOutsideWindow`:
    - Setup: the future one-minute window exactly as in `TestRunDispatchAutoOutsideWindowSkipsBeforeFetching`, `dispatchAuto = true`, no GitHub env.
    - Expected: `Outcome "outside_window"` and empty `LanesInWindow`.
  - `TestRunDispatch_ConfigUnreadable_JournalsError`:
    - Setup: write `{not json` to `dispatchConfigPath()`.
    - Expected: `runDispatch` returns an error; the record has `Outcome "error"` and a non-empty `Error`.
  - `TestRunDispatch_RetryOnlyDryRun_JournalsRetryOnly`:
    - Setup: `pinUnattendedSchedule`, `dispatchRetryOnly = true`, dry-run, fake server as above.
    - Expected: `RetryOnly == true`, `Mode "dry-run"`, and no decision for `#41` (retry-only skips new issues).
  - `TestRunDispatch_JournalDirUnwritable_TickUnaffected`:
    - Setup: create `XDG_CACHE_HOME/bridge/dispatch-journal` as a **regular file**. Then run a live `--json` tick with `BRIDGE_BASE` an empty temp dir (no repos), in the same setup as `TestRunDispatch_JSONLiveTick_CallsApplyDecisions`.
    - Expected: `runDispatch` returns nil, and stdout decodes as a JSON array.
- [ ] **Step 2: Run to verify failure:** `go test ./cmd/bridge -run 'Journals|JournalDirUnwritable' -v` → FAIL (no records).
- [ ] **Step 3: Implement** the changes above.
- [ ] **Step 4: Run the gates.** Run the full `go test -race ./...` once. Expected: green, with every pre-existing `dispatch_test.go` / `dispatch_retry_test.go` test unmodified.
- [ ] **Step 5: Commit**
```bash
git add cmd/bridge/dispatch.go cmd/bridge/dispatch_journal.go cmd/bridge/dispatch_journal_test.go
git commit -m "feat(dispatch): journal every tick on the shared tick path (#379)"
```

### Task 4: Docs and changelog

**Files:**
- Modify: `docs/dispatch.md` (a new `## Dispatch journal` section before `## Running it`; one sentence in `### Systemd timer and service`), `CHANGELOG.md` (`[Unreleased]` → `### Added`)

- [ ] **Step 1:** In `docs/dispatch.md`, add `## Dispatch journal` covering:
  - the path `~/.cache/bridge/dispatch-journal/YYYY-MM-DD.jsonl` (the UTC day, or under `$XDG_CACHE_HOME/bridge/`);
  - one JSON line per invocation;
  - `trigger` (`timer` = `--auto`, else `manual`) versus `mode` (`--dry-run`), with the note that the observation-week timer writes `timer` + `dry-run`;
  - the `outcome` values, the `decision` values (with `not_applied` explained) and the `budget` reading;
  - 30-day retention pruned per file;
  - that it is append-only and safe to read while a tick runs (skip a partial last line);
  - that journaling failures only warn in `journalctl`.

  Include one example record. In the systemd section, add one sentence that every timer tick, dry-run included, is journaled.
- [ ] **Step 2:** CHANGELOG `Added`: "`bridge dispatch`: every tick appends a record to a per-day JSON-lines **dispatch journal** under the bridge cache dir (trigger, mode, lanes in window, repos, budget reading, each decision with its outcome), kept 30 days; the cockpit digest reads it. (#379)"
- [ ] **Step 3:** Run `gofmt -l .`, `go vet ./...` and `golangci-lint run`, then `grep -n "Dispatch journal" docs/dispatch.md`.
- [ ] **Step 4: Commit**
```bash
git add docs/dispatch.md CHANGELOG.md
git commit -m "docs(dispatch): document the per-tick dispatch journal (#379)"
```
