package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/usage"
)

func renderDecisions(w io.Writer, ds []dispatch.Decision) {
	dispatched, skipped := 0, 0
	for _, d := range ds {
		status := "dispatch"
		if !d.Dispatch {
			status = fmt.Sprintf("SKIP (%s)", d.Reason)
			skipped++
		} else {
			dispatched++
		}
		fmt.Fprintf(w, "  %-12s #%-4d %-28s → %s\n",
			d.Candidate.Repo, d.Candidate.Issue.Number, truncate(d.Candidate.Issue.Title, 28), status)
	}
	fmt.Fprintf(w, "\n%d dispatched, %d skipped\n", dispatched, skipped)
}

// repoInput is one repo's fetched state, kept as a plain struct so
// collectCandidates stays testable without a network.
type repoInput struct {
	Forge      string
	Owner      string
	Name       string
	Issues     []forge.Issue
	Milestones []forge.Milestone
	PRs        []forge.PullRequest
}

// collectCandidates filters each repo's issues to the eligible ones.
// Non-GitHub repos are skipped silently: ai-implement runs on GitHub Actions,
// so there is no pipeline to dispatch to elsewhere.
func collectCandidates(repos []repoInput) []dispatch.Candidate {
	var out []dispatch.Candidate
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		active := dispatch.ActiveMilestone(r.Milestones)
		due := milestoneDue(r.Milestones, active)
		for _, i := range r.Issues {
			if ok, _ := dispatch.Eligible(i, active, r.PRs); !ok {
				continue
			}
			out = append(out, dispatch.Candidate{
				Issue: i, Owner: r.Owner, Repo: r.Name, MilestoneDue: due,
			})
		}
	}
	return out
}

func milestoneDue(ms []forge.Milestone, title string) time.Time {
	for _, m := range ms {
		if m.Title == title {
			return m.DueOn
		}
	}
	return time.Time{}
}

var (
	dispatchDryRun bool
	dispatchJSON   bool
	dispatchAuto   bool
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

// measureWindowUSD sums interactive and pipeline consumption over the trailing
// quota window. The bool reports whether the number can be trusted: false means
// fail closed, and is never the same as a measured zero.
func measureWindowUSD(ctx context.Context, cfg dispatch.Config, from, to time.Time) (float64, bool) {
	turns, err := usage.ScanTranscripts(ctx, transcriptRoot(), from)
	if err != nil {
		slog.Warn("dispatch: cannot read Claude transcripts — guarded dispatch will be blocked", "error", err)
		return 0, false
	}
	ledger, err := usage.LoadLedger(dispatchLedgerPath())
	if err != nil {
		slog.Warn("dispatch: cannot read the usage ledger — guarded dispatch will be blocked", "error", err)
		return 0, false
	}

	pricing := usage.DefaultPricing().Merge(cfg.Budget.Pricing)
	return usage.SumWindow(turns, pricing, from, to) + ledger.SumSince(from), true
}

// tickTiming resolves one instant against the configured schedule: which
// window covers it, what the budget rung is guarding, the span to measure
// usage over, and which window occurrence the nightly counter belongs to.
type tickTiming struct {
	Now             time.Time
	Window          dispatch.Window
	InWindow        bool
	RungOn          bool
	Guard           time.Time // the instant the rung protects
	MeasureFrom     time.Time
	NightCapApplies bool
	NightStart      time.Time
}

// resolveTiming is the single place the schedule is interpreted, so a tick and
// a `status` read can never disagree about which bounds are in force.
func resolveTiming(cfg dispatch.Config, now time.Time) tickTiming {
	t := tickTiming{Now: now}
	t.Window, t.InWindow = cfg.Schedule.InWindow(now)

	guard, on := cfg.Schedule.RungGuard(now, cfg.Budget.WindowHours)
	t.RungOn = on
	t.Guard = guard
	if on {
		// Measure the quota window that ends at the guarded instant. Inside a
		// rung window that is now; in the pre-dawn shoulder it is the coming
		// handover, so only spend still inside the window at that point counts.
		t.MeasureFrom = guard.Add(-time.Duration(cfg.Budget.WindowHours * float64(time.Hour)))
	}

	// The nightly ceiling bounds unattended spend, so it belongs to a window
	// whose rung is off — and its counter resets at that window's own start.
	if t.InWindow && !t.Window.BudgetRung {
		t.NightCapApplies = true
		t.NightStart = t.Window.StartOf(now)
	}
	return t
}

func runDispatch(cmd *cobra.Command, _ []string) error {
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
		return nil
	}

	now := time.Now()
	timing := resolveTiming(cfg, now)

	ctx := context.Background()
	repos, err := fetchRepoInputs(ctx)
	if err != nil {
		return err
	}

	gate := resolveGate(ctx, repos, cfg.Lanes, now)
	candidates := collectCandidates(repos)
	assignLanes(candidates, cfg.Lanes, gate)
	acting, outOfWindow := dispatch.PartitionByWindow(
		dispatch.Order(candidates, cfg.RepoPriority), cfg.Schedule, now)

	// The window gate is --auto only, as before: an explicit `dispatch now` is
	// the operator asking for a tick. With lanes the question is per lane, so
	// the tick stops only when no lane acts at all.
	if dispatchAuto && len(acting) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "outside dispatch window — no lane acts right now")
		return nil
	}

	// The rung is keyed on what it guards, not on --auto: a manual tick burns
	// the same quota, and a pre-dawn tick burns the window the operator will
	// inherit at the handover. It is off only when now is genuinely outside
	// every rung window and its shoulder.
	usedUSD, usedKnown := 0.0, true
	if timing.RungOn {
		usedUSD, usedKnown = measureWindowUSD(ctx, cfg, timing.MeasureFrom, now)
	}
	budget := dispatch.NewBudgetState(cfg.Budget, timing.RungOn, usedUSD, usedKnown)

	autonomous := func(repo string) bool {
		lane, _ := dispatch.ResolveLane(cfg.Lanes, repo, gate)
		return lane.Autonomous
	}
	openByRepo, globalOpen := countOpenAgentPRs(repos, autonomous)

	decisions := dispatch.ApplyCaps(acting, cfg,
		dispatch.Counts{
			OpenPRsByRepo:     openByRepo,
			GlobalOpen:        globalOpen,
			DispatchedTonight: state.DispatchesSince(nightStartForReport(cfg, timing, now)),
			NightCapApplies:   timing.NightCapApplies,
			DispatchedByLane:  laneCounts(state, cfg, now),
		},
		budget,
	)
	decisions = append(decisions, outOfWindow...)

	if dispatchJSON {
		if err := emitJSON(cmd.OutOrStdout(), decisions); err != nil {
			return err
		}
	} else {
		renderDecisions(cmd.OutOrStdout(), decisions)
	}
	if dispatchDryRun {
		return nil
	}
	return applyDecisions(ctx, decisions, state, timing, cfg)
}

// fetchRepoInputs reads every discovered repo's issues, milestones and open
// PRs. Mirror the error handling in cmd/bridge/issues.go: keep the first
// error, skip the failing repo, keep going — one unreachable repo must not
// stop the whole tick.
func fetchRepoInputs(ctx context.Context) ([]repoInput, error) {
	repos, err := discoverAllRoots()
	if err != nil {
		return nil, err
	}
	var out []repoInput
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
		in := repoInput{Forge: r.Forge, Owner: r.Owner, Name: r.Name}
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

// assignLanes resolves each candidate's lane in place, before ordering, so the
// cap walk and the label write read the same decision.
func assignLanes(cs []dispatch.Candidate, lanes []dispatch.Lane, gate dispatch.GateState) {
	for i := range cs {
		cs[i].Lane, cs[i].LaneReason = dispatch.ResolveLane(lanes, cs[i].Repo, gate)
	}
}

// countOpenAgentPRs counts open PRs that close one of the repo's own issues.
// Only those are pipeline output, so a hand-written PR never consumes a slot.
//
// An autonomous lane's PRs are left out of the global total entirely: that cap
// is the operator's review capacity, and nobody reviews them. They still count
// per repo, where the bound is conflicting concurrent PRs rather than review.
func countOpenAgentPRs(repos []repoInput, autonomous func(repo string) bool) (map[string]int, int) {
	byRepo := make(map[string]int, len(repos))
	total := 0
	for _, r := range repos {
		for _, pr := range r.PRs {
			for _, i := range r.Issues {
				if dispatch.ClosesIssue(pr.Body, i.Number) {
					byRepo[r.Name]++
					if !autonomous(r.Name) {
						total++
					}
					break
				}
			}
		}
	}
	return byRepo, total
}

// laneCounts reads each configured lane's spent counter for the occurrence it
// is currently in. The implicit default lane is bounded by the nightly cap, not
// by a lane ceiling, so it needs no entry here.
func laneCounts(state dispatch.State, cfg dispatch.Config, now time.Time) map[string]int {
	out := make(map[string]int, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		out[l.Name] = state.DispatchesInLane(l.Name, l.WindowStart(cfg.Schedule, now))
	}
	return out
}

// applyDecisions writes the one label the dispatcher owns, then persists the
// ledger and the nightly counter. It never writes agent:* or model:* — model
// choice belongs to agent-workflow's classify-task.sh.
func applyDecisions(ctx context.Context, ds []dispatch.Decision, state dispatch.State, timing tickTiming, cfg dispatch.Config) error {
	var runs []usage.Run
	var dispatchErr error

	laneDispatched := map[string]int{}
	for _, d := range ds {
		if !d.Dispatch {
			continue
		}
		lane := d.Candidate.Lane
		if lane.DryRun {
			// The lane is being observed, not run. It applies nothing, books no
			// spend, and advances no counter — see the spec's dry_run section.
			continue
		}
		gh, ok := clientFor("github").(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		owner, repo, num := d.Candidate.Owner, d.Candidate.Repo, d.Candidate.Issue.Number
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

	return errors.Join(dispatchErr, ledgerErr, stateErr)
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
	timing := resolveTiming(cfg, now)
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
			Limits            dispatch.Limits `json:"limits"`
			Paused            bool            `json:"paused"`
			DispatchedTonight int             `json:"dispatched_tonight"`
			LastTick          time.Time       `json:"last_tick,omitempty"`
			BudgetWindowHours float64         `json:"budget_window_hours"`
			BudgetUsedUSD     float64         `json:"budget_used_usd"`
			BudgetLimitUSD    float64         `json:"budget_limit_usd"`
			BudgetKnown       bool            `json:"budget_known"`
			BudgetRungActive  bool            `json:"budget_rung_active"`
			NightCapApplies   bool            `json:"night_cap_applies"`
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
		})
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "paused: %t\n", state.Paused)
	// Report the count against the most recent unattended window either way:
	// "how many did it dispatch overnight?" is a fair question at 09:00, and
	// answering n/a made the number unrecoverable from the CLI. Only the label
	// changes with whether the cap is currently in force.
	nightCount := state.DispatchesSince(nightStartForReport(cfg, timing, now))
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
	fmt.Fprintf(w, "budget rung: %s\n", rungLabel(timing))
	if state.LastTick.IsZero() {
		fmt.Fprintln(w, "last tick: never")
	} else {
		fmt.Fprintf(w, "last tick: %s\n", state.LastTick.Format(time.RFC3339))
	}
	return nil
}

// nightStartForReport resolves the occurrence the nightly counter belongs to
// for display. Inside an unattended window that is the tick's own boundary;
// elsewhere it is the latest unattended window's start, so `status` can still
// report what the last night spent instead of dropping the number.
func nightStartForReport(cfg dispatch.Config, timing tickTiming, now time.Time) time.Time {
	if !timing.NightStart.IsZero() {
		return timing.NightStart
	}
	var latest time.Time
	for _, w := range cfg.Schedule.Windows {
		if w.BudgetRung {
			continue
		}
		if s := w.StartOf(now); !s.IsZero() && (latest.IsZero() || s.After(latest)) {
			latest = s
		}
	}
	return latest
}

// rungLabel describes whether the budget rung is policing this moment, and
// which window that decision came from.
func rungLabel(t tickTiming) string {
	switch {
	case t.RungOn && t.InWindow && t.Window.BudgetRung:
		return fmt.Sprintf("active (window %s-%s)", t.Window.From, t.Window.To)
	case t.RungOn:
		// The shoulder: still in an unguarded window, but close enough to the
		// handover that this spend survives into the operator's quota window.
		return fmt.Sprintf("active (reserving headroom for the %s handover)",
			t.Guard.Format("15:04"))
	case !t.InWindow:
		return "inactive (outside every configured window)"
	default:
		return fmt.Sprintf("inactive (window %s-%s)", t.Window.From, t.Window.To)
	}
}
