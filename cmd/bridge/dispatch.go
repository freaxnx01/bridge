package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/dispatchrun"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/usage"
)

// decisionStatus renders one decision's outcome. A lane downgrade always shows,
// dispatch or skip: "why is this repo not in the auto lane" is the question the
// observation week exists to answer.
func decisionStatus(d dispatch.Decision) string {
	reason := d.Candidate.LaneReason
	if d.Candidate.Retry {
		return retryStatus(d, reason)
	}
	switch {
	case d.Dispatch && d.Candidate.Lane.DryRun:
		return withReason("WOULD dispatch (dry-run)", reason)
	case d.Dispatch:
		return withReason("dispatch", reason)
	default:
		if reason != "" {
			return fmt.Sprintf("SKIP (%s; %s)", reason, d.Reason)
		}
		return fmt.Sprintf("SKIP (%s)", d.Reason)
	}
}

// retryStatus renders a re-dispatch, which always names the failure the retry
// is answering: "retry" alone would not say what is being retried, and that is
// the one thing an operator reading an unattended tick needs.
func retryStatus(d dispatch.Decision, laneReason string) string {
	switch {
	case d.Dispatch && d.Candidate.Lane.DryRun:
		return withReason("WOULD retry: "+d.Candidate.RetryReason+" (dry-run)", laneReason)
	case d.Dispatch:
		return withReason("retry: "+d.Candidate.RetryReason, laneReason)
	case laneReason != "":
		return fmt.Sprintf("SKIP retry (%s; %s)", laneReason, d.Reason)
	default:
		return fmt.Sprintf("SKIP retry (%s)", d.Reason)
	}
}

func withReason(status, reason string) string {
	if reason == "" {
		return status
	}
	return fmt.Sprintf("%s (%s)", status, reason)
}

func renderDecisions(w io.Writer, ds []dispatch.Decision) {
	dispatched, skipped := 0, 0
	for _, d := range ds {
		if d.Dispatch {
			dispatched++
		} else {
			skipped++
		}
		fmt.Fprintf(w, "  %-16s #%-4d %-28s %-6s → %s\n",
			d.Candidate.Repo, d.Candidate.Issue.Number,
			truncate(d.Candidate.Issue.Title, 28),
			d.Candidate.Lane.Name, decisionStatus(d))
	}
	fmt.Fprintf(w, "\n%d dispatched, %d skipped\n", dispatched, skipped)
}

// decisionJSON is the machine-readable view of a decision. Decision itself
// carries no json tags and nests a whole forge.Issue; a named view keeps the
// documented shape stable as the struct grows.
type decisionJSON struct {
	Repo       string   `json:"repo"`
	Issue      int      `json:"issue"`
	Title      string   `json:"title"`
	Lane       string   `json:"lane"`
	LaneReason string   `json:"lane_reason,omitempty"`
	Labels     []string `json:"labels"`
	DryRun     bool     `json:"dry_run"`
	Dispatch   bool     `json:"dispatch"`
	Reason     string   `json:"reason,omitempty"`
	// Kind separates a first dispatch from a re-dispatch; RetryReason is the
	// failure the retry answers. Reason is already the skip reason, and a retry
	// skipped by a cap needs both.
	Kind        string `json:"kind"`
	RetryReason string `json:"retry_reason,omitempty"`
	// Rank is the candidate's 1-based place in the tick's order, RankReason the
	// ladder rung it differs from the next candidate on, and Ladder its reading
	// on every rung — the three answer "why here?" without re-deriving the
	// ladder from labels.
	Rank       int             `json:"rank"`
	RankReason string          `json:"rank_reason,omitempty"`
	Ladder     dispatch.Ladder `json:"ladder"`
}

// decisionKind names the pass a decision came from.
func decisionKind(c dispatch.Candidate) string {
	if c.Retry {
		return "retry"
	}
	return "dispatch"
}

func decisionsJSON(ds []dispatch.Decision) []decisionJSON {
	out := make([]decisionJSON, 0, len(ds))
	for _, d := range ds {
		out = append(out, decisionJSON{
			Repo:       d.Candidate.Repo,
			Issue:      d.Candidate.Issue.Number,
			Title:      d.Candidate.Issue.Title,
			Lane:       d.Candidate.Lane.Name,
			LaneReason: d.Candidate.LaneReason,
			Labels:     d.Candidate.Lane.EffectiveLabels(),
			DryRun:     d.Candidate.Lane.DryRun,
			Dispatch:   d.Dispatch,
			Reason:     d.Reason,

			Kind:        decisionKind(d.Candidate),
			RetryReason: d.Candidate.RetryReason,

			Rank:       d.Rank.Position,
			RankReason: d.Rank.Reason,
			Ladder:     d.Rank.Ladder,
		})
	}
	return out
}

var (
	dispatchDryRun    bool
	dispatchJSON      bool
	dispatchAuto      bool
	dispatchRetryOnly bool
)

var dispatchCmd = &cobra.Command{
	Use:   "dispatch",
	Short: "Dispatch eligible issues to the agent-workflow pipeline",
	RunE:  runDispatch,
}

var dispatchNowCmd = &cobra.Command{
	Use:   "now",
	Short: "Run one dispatch tick immediately",
	RunE:  func(cmd *cobra.Command, args []string) error { return runDispatch(cmd, args) },
}

var dispatchPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Stop the dispatcher until resumed",
	RunE:  func(cmd *cobra.Command, args []string) error { return setPaused(cmd, true) },
}

var dispatchResumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume the dispatcher",
	RunE:  func(cmd *cobra.Command, args []string) error { return setPaused(cmd, false) },
}

var dispatchStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show caps, in-flight work, and last tick",
	RunE:  runDispatchStatus,
}

func init() {
	dispatchCmd.PersistentFlags().BoolVar(&dispatchDryRun, "dry-run", false, "decide and print, change nothing")
	dispatchCmd.PersistentFlags().BoolVar(&dispatchJSON, "json", false, "machine-readable output")
	dispatchCmd.PersistentFlags().BoolVar(&dispatchAuto, "auto", false, "timer entry point; honours the pause flag")
	dispatchCmd.PersistentFlags().BoolVar(&dispatchRetryOnly, "retry-only", false, "run only the retry pass")
	dispatchCmd.AddCommand(dispatchNowCmd, dispatchPauseCmd, dispatchResumeCmd, dispatchStatusCmd)
	rootCmd.AddCommand(dispatchCmd)
}

func dispatchConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "bridge", "dispatch.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bridge", "dispatch.json")
}

func dispatchStatePath() string { return filepath.Join(cacheRoot(), "dispatch.json") }

func dispatchLedgerPath() string { return filepath.Join(cacheRoot(), "usage.json") }

// transcriptRoot is where Claude Code writes its session transcripts. The env
// override exists so tests never read the operator's real history.
func transcriptRoot() string {
	if v := os.Getenv("BRIDGE_CLAUDE_PROJECTS"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// measureWindowUSD sums the trailing quota window from this host's transcript
// root and run ledger.
func measureWindowUSD(ctx context.Context, cfg dispatch.Config, from, to time.Time) (float64, bool) {
	return dispatchrun.MeasureWindowUSD(ctx, cfg, transcriptRoot(), dispatchLedgerPath(), from, to)
}

// runDispatch runs one tick and journals it. Every exit path of runTick lands
// in exactly one record, so the cockpit digest can account for every slot —
// and journaling never changes the tick's own result.
func runDispatch(cmd *cobra.Command, _ []string) error {
	rec := newTickRecord(time.Now(), dispatchAuto, dispatchDryRun, dispatchRetryOnly)
	err := runTick(cmd, &rec)
	recordTick(dispatchJournalDir(), rec, err)
	return err
}

// runTick is the tick itself. It fills rec as it goes: the outcome at each
// early exit, and the tick's inputs and decisions once they are known.
func runTick(cmd *cobra.Command, rec *dispatch.JournalRecord) error {
	cfg, err := dispatch.LoadConfig(dispatchConfigPath())
	if err != nil {
		return err
	}
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		return err
	}
	// --auto is the only mode the pause flag gates. An explicit `dispatch now`
	// is the operator asking for it, so it always runs.
	if dispatchAuto && state.Paused {
		fmt.Fprintln(cmd.OutOrStdout(), "dispatcher paused — nothing to do")
		rec.Outcome = dispatch.OutcomePaused
		return nil
	}

	now := time.Now()
	timing := dispatch.ResolveTiming(cfg, now)
	rec.At = now
	rec.LanesInWindow = lanesInWindow(cfg, now)

	// The window gate is --auto only, as before: an explicit `dispatch now` is
	// the operator asking for a tick. With lanes the question is per lane, so
	// the tick stops only when no lane acts at all — and that question is
	// answered from the config alone, before any network call, which keeps an
	// out-of-window tick the free no-op it was before lanes.
	if dispatchAuto && !dispatch.AnyLaneActs(cfg, now) {
		fmt.Fprintln(cmd.OutOrStdout(), "outside dispatch window — no lane acts right now")
		rec.Outcome = dispatch.OutcomeOutsideWindow
		return nil
	}

	ctx := context.Background()
	repos, err := fetchRepoInputs(ctx)
	if err != nil {
		return err
	}
	rec.Repos = repoNames(repos)

	gate := resolveGate(ctx, repos, cfg.Lanes, now)
	retries, retrySkips := collectRetriesForTick(ctx, repos, now)

	// The rung is keyed on what it guards, not on --auto: a manual tick burns
	// the same quota, and a pre-dawn tick burns the window the operator will
	// inherit at the handover. It is off only when now is genuinely outside
	// every rung window and its shoulder.
	usedUSD, usedKnown := 0.0, true
	if timing.RungOn {
		usedUSD, usedKnown = measureWindowUSD(ctx, cfg, timing.MeasureFrom, now)
	}

	// The window gate stays --auto only, as before: GateNone is the manual
	// tick's "decide everything, the operator asked for it".
	mode := dispatch.GateNone
	if dispatchAuto {
		mode = dispatch.GateNow
	}
	tick := dispatch.Evaluate(dispatch.TickInput{
		Repos: repos, Config: cfg, State: state, Gate: gate,
		Retries: retries, RetrySkips: retrySkips, RetryOnly: dispatchRetryOnly,
		UsedUSD: usedUSD, UsedKnown: usedKnown, Now: now, Mode: mode,
	})
	decisions := tick.Decisions
	if timing.RungOn {
		rec.Budget = journalBudget(tick.Gates)
	}
	rec.Gates = &tick.Gates
	rec.Decisions = journalDecisions(decisions, dispatchDryRun)

	if dispatchJSON {
		if err := emitJSON(cmd.OutOrStdout(), decisionsJSON(decisions)); err != nil {
			return err
		}
	} else {
		renderDecisions(cmd.OutOrStdout(), decisions)
	}
	if dispatchDryRun {
		return nil
	}
	runs, err := applyDecisionsBooked(ctx, decisions, state, timing, cfg)
	// The ledger is what makes a dispatch real, so the journal promotes only
	// the runs it booked — a label that never landed stays not_applied.
	markBooked(rec.Decisions, runs)
	return err
}

// fetchRepoInputs reads every discovered repo's issues, milestones and open
// PRs. Mirror the error handling in cmd/bridge/issues.go: keep the first
// error, skip the failing repo, keep going — one unreachable repo must not
// stop the whole tick.
func fetchRepoInputs(ctx context.Context) ([]dispatch.RepoInput, error) {
	repos, err := discoverAllRoots()
	if err != nil {
		return nil, err
	}
	var out []dispatch.RepoInput
	var firstErr error
	githubRepos, clientsResolved := 0, 0
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		githubRepos++
		gh, ok := clientFor(r.Forge).(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		clientsResolved++
		in := dispatch.RepoInput{Forge: r.Forge, Owner: r.Owner, Name: r.Name}
		if in.Issues, err = gh.ListOpenIssues(ctx, r.Owner, r.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if in.Milestones, err = gh.ListOpenMilestones(ctx, r.Owner, r.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if in.PRs, err = gh.ListOpenPullRequests(ctx, r.Owner, r.Name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, in)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	if firstErr != nil {
		slog.Warn("dispatch: skipped repo(s) due to fetch error", "error", firstErr)
	}
	if githubRepos > 0 && clientsResolved == 0 {
		slog.Warn("dispatch: no GitHub client available — check GH_TOKEN/BRIDGE_GITHUB_API", "github_repos", githubRepos)
	}
	return out, nil
}

// applyDecisions writes the one label the dispatcher owns, then persists the
// ledger and the nightly counter. It never writes agent:* or model:* — model
// choice belongs to agent-workflow's classify-task.sh.
func applyDecisions(ctx context.Context, ds []dispatch.Decision, state dispatch.State, timing dispatch.Timing, cfg dispatch.Config) error {
	_, err := applyDecisionsBooked(ctx, ds, state, timing, cfg)
	return err
}

// applyDecisionsBooked is applyDecisions plus the runs it booked. The journal
// needs that list to tell a dispatch that happened from one the tick only
// meant to make: booking is the same line the ledger draws.
func applyDecisionsBooked(ctx context.Context, ds []dispatch.Decision, state dispatch.State, timing dispatch.Timing, cfg dispatch.Config) ([]usage.Run, error) {
	var runs []usage.Run
	var dispatchErr error

	laneDispatched := map[string]int{}
	for _, d := range ds {
		if !d.Dispatch {
			continue
		}
		lane := d.Candidate.Lane
		if lane.DryRun {
			// The lane is being observed, not run: it applies no label and
			// books no spend. Its own counter still advances and still
			// persists, so `max_dispatches` bounds the observation week the
			// way it will bound the live lane — otherwise every tick reports
			// a full lane's worth of decisions and `status` reads 0 spent.
			laneDispatched[lane.Name]++
			continue
		}
		gh, ok := clientFor("github").(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		owner, repo, num := d.Candidate.Owner, d.Candidate.Repo, d.Candidate.Issue.Number
		if d.Candidate.Retry {
			booked, err := applyRetry(ctx, gh, d)
			if booked {
				runs = append(runs, usage.Run{
					At: timing.Now, Repo: repo, Issue: num, EstUSD: cfg.Budget.MeanRunCostUSD,
				})
				laneDispatched[lane.Name]++
			}
			if err != nil {
				dispatchErr = err
				break
			}
			continue
		}
		if _, err := gh.AddLabels(ctx, owner, repo, num, lane.EffectiveLabels()); err != nil {
			dispatchErr = fmt.Errorf("label %s#%d: %w", repo, num, err)
			break
		}
		// The label is the pipeline's trigger, so from here the run is real and
		// has to be booked even if the follow-up comment fails. Booking only on
		// the all-succeeded path under-counted spend whenever a forge call
		// failed mid-loop — the fail-open direction the rung exists to prevent.
		runs = append(runs, usage.Run{
			At: timing.Now, Repo: repo, Issue: num, EstUSD: cfg.Budget.MeanRunCostUSD,
		})
		laneDispatched[lane.Name]++
		if _, err := gh.CommentIssue(ctx, owner, repo, num,
			"Dispatched by `bridge dispatch`."); err != nil {
			dispatchErr = fmt.Errorf("comment %s#%d: %w", repo, num, err)
			break
		}
	}

	// Persist what actually happened before surfacing any dispatch error: the
	// labels are already on the forge, so dropping their accounting would let
	// the next tick spend the same headroom twice.
	//
	// The two stores are independent on purpose — a failing ledger write must
	// not also discard the nightly counter, or three issues labelled at 23:00
	// with a full cache disk would lose both records and let the 00:00 tick
	// dispatch three more past the cap. Fail-open in two dimensions at once.
	ledgerErr := recordRuns(dispatchLedgerPath(), runs, timing.Now)

	if timing.NightCapApplies {
		base := state.DispatchesSince(timing.NightStart)
		if base == 0 {
			state.NightStartedAt = timing.Now
		}
		state.DispatchedTonight = base + len(runs)
	}
	for _, l := range cfg.Lanes {
		if n := laneDispatched[l.Name]; n > 0 {
			state.RecordLaneDispatch(l.Name, l.WindowStart(cfg.Schedule, timing.Now), timing.Now, n)
		}
	}
	state.LastTick = timing.Now
	stateErr := dispatch.WriteState(dispatchStatePath(), state)

	return runs, errors.Join(dispatchErr, ledgerErr, stateErr)
}

// recordRuns appends runs to the ledger and trims history outside the window
// anyone reads.
//
// The reload sits immediately before the write to keep the read-modify-write
// window as small as possible. It does not close it: an overlapping writer's
// runs can still be lost. A file lock would, but needs per-platform syscalls
// and bridge ships a Windows build — disproportionate for an hourly timer
// racing a hand-run command, and the failure is a slight under-count rather
// than a corrupt file.
func recordRuns(path string, runs []usage.Run, now time.Time) error {
	if len(runs) == 0 {
		return nil
	}
	ledger, err := usage.LoadLedger(path)
	if err != nil {
		return fmt.Errorf("read usage ledger: %w", err)
	}
	for _, r := range runs {
		ledger.Append(r)
	}
	// Only the trailing window is ever summed; a week of history is plenty for
	// calibration and keeps the file bounded.
	ledger.Prune(now.AddDate(0, 0, -7))
	if err := usage.WriteLedger(path, ledger); err != nil {
		return fmt.Errorf("write usage ledger: %w", err)
	}
	return nil
}

// setPaused flips the dispatcher's paused flag in local state and reports the
// new state. No network calls: pause/resume only ever touch the local cache
// file that --auto reads before each tick.
func setPaused(cmd *cobra.Command, paused bool) error {
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		return err
	}
	state.Paused = paused
	if err := dispatch.WriteState(dispatchStatePath(), state); err != nil {
		return err
	}
	verb := "resumed"
	if paused {
		verb = "paused"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "dispatcher %s\n", verb)
	return nil
}

// runDispatchStatus reports the configured caps and local dispatch state.
// It makes no network call: in-flight PR counts would require a repo fetch,
// which is out of scope for a v1 status command. The trailing-window usage it
// reports comes from local sources only (transcripts + the run ledger), which
// keeps that property intact.
func runDispatchStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := dispatch.LoadConfig(dispatchConfigPath())
	if err != nil {
		return err
	}
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		return err
	}

	now := time.Now()
	timing := dispatch.ResolveTiming(cfg, now)
	measureFrom := timing.MeasureFrom
	if measureFrom.IsZero() {
		// Unguarded right now, but the operator still wants a reading, so show
		// the plain trailing window.
		measureFrom = now.Add(-time.Duration(cfg.Budget.WindowHours * float64(time.Hour)))
	}
	usedUSD, usedKnown := measureWindowUSD(context.Background(), cfg, measureFrom, now)
	budget := dispatch.NewBudgetState(cfg.Budget, timing.RungOn, usedUSD, usedKnown)

	if dispatchJSON {
		return emitJSON(cmd.OutOrStdout(), struct {
			Limits            dispatch.Limits  `json:"limits"`
			Paused            bool             `json:"paused"`
			DispatchedTonight int              `json:"dispatched_tonight"`
			LastTick          time.Time        `json:"last_tick,omitempty"`
			BudgetWindowHours float64          `json:"budget_window_hours"`
			BudgetUsedUSD     float64          `json:"budget_used_usd"`
			BudgetLimitUSD    float64          `json:"budget_limit_usd"`
			BudgetKnown       bool             `json:"budget_known"`
			BudgetRungActive  bool             `json:"budget_rung_active"`
			NightCapApplies   bool             `json:"night_cap_applies"`
			Lanes             []laneStatusJSON `json:"lanes,omitempty"`
		}{
			Limits:            cfg.Limits,
			Paused:            state.Paused,
			DispatchedTonight: state.DispatchesSince(timing.NightStart),
			LastTick:          state.LastTick,
			BudgetWindowHours: cfg.Budget.WindowHours,
			BudgetUsedUSD:     budget.UsedUSD,
			BudgetLimitUSD:    budget.LimitUSD,
			BudgetKnown:       !budget.Unknown,
			BudgetRungActive:  budget.Enabled,
			NightCapApplies:   timing.NightCapApplies,
			Lanes:             laneStatuses(cfg, state, now),
		})
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "paused: %t\n", state.Paused)
	// Report the count against the most recent unattended window either way:
	// "how many did it dispatch overnight?" is a fair question at 09:00, and
	// answering n/a made the number unrecoverable from the CLI. Only the label
	// changes with whether the cap is currently in force.
	nightCount := state.DispatchesSince(dispatch.NightStartForReport(cfg, timing, now))
	if timing.NightCapApplies {
		fmt.Fprintf(w, "dispatched tonight: %d/%d\n", nightCount, cfg.Limits.MaxDispatchesPerNight)
	} else {
		fmt.Fprintf(w, "dispatched last night: %d/%d (cap not in force for this window)\n",
			nightCount, cfg.Limits.MaxDispatchesPerNight)
	}
	fmt.Fprintf(w, "per-repo cap: %d, global cap: %d\n", cfg.Limits.PerRepo, cfg.Limits.GlobalOpenPRs)
	if budget.Unknown {
		fmt.Fprintf(w, "budget window: %gh — usage unreadable, daytime dispatch blocked\n", cfg.Budget.WindowHours)
	} else {
		pct := 0.0
		if budget.LimitUSD > 0 {
			pct = budget.UsedUSD / budget.LimitUSD * 100
		}
		fmt.Fprintf(w, "budget window: %gh — used $%.2f of $%.2f (%.0f%%)\n",
			cfg.Budget.WindowHours, budget.UsedUSD, budget.LimitUSD, pct)
	}
	fmt.Fprintf(w, "budget rung: %s\n", dispatch.RungLabel(timing))
	if state.LastTick.IsZero() {
		fmt.Fprintln(w, "last tick: never")
	} else {
		fmt.Fprintf(w, "last tick: %s\n", state.LastTick.Format(time.RFC3339))
	}
	for _, l := range cfg.Lanes {
		start := l.WindowStart(cfg.Schedule, now)
		spent := state.DispatchesInLane(l.Name, start)
		mode := "live"
		if l.DryRun {
			mode = "dry-run"
		}
		ceiling := "∞"
		if l.Limits.MaxDispatches > 0 {
			ceiling = strconv.Itoa(l.Limits.MaxDispatches)
		}
		fmt.Fprintf(w, "lane %s: %s, autonomous=%t, dispatched %d/%s this window\n",
			l.Name, mode, l.Autonomous, spent, ceiling)
	}
	return nil
}

// laneStatusJSON reports one lane's configuration and spent counter.
type laneStatusJSON struct {
	Name          string `json:"name"`
	Autonomous    bool   `json:"autonomous"`
	DryRun        bool   `json:"dry_run"`
	Dispatched    int    `json:"dispatched"`
	MaxDispatches int    `json:"max_dispatches,omitempty"`
	InWindow      bool   `json:"in_window"`
}

func laneStatuses(cfg dispatch.Config, state dispatch.State, now time.Time) []laneStatusJSON {
	out := make([]laneStatusJSON, 0, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		out = append(out, laneStatusJSON{
			Name:          l.Name,
			Autonomous:    l.Autonomous,
			DryRun:        l.DryRun,
			Dispatched:    state.DispatchesInLane(l.Name, l.WindowStart(cfg.Schedule, now)),
			MaxDispatches: l.Limits.MaxDispatches,
			InWindow:      l.InWindow(cfg.Schedule, now),
		})
	}
	return out
}
