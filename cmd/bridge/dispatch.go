package main

import (
	"context"
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
func measureWindowUSD(ctx context.Context, cfg dispatch.Config, now time.Time) (float64, bool) {
	since := now.Add(-time.Duration(cfg.Budget.WindowHours * float64(time.Hour)))

	turns, err := usage.ScanTranscripts(ctx, transcriptRoot(), since)
	if err != nil {
		slog.Warn("dispatch: cannot read Claude transcripts — daytime dispatch will be blocked", "error", err)
		return 0, false
	}
	ledger, err := usage.LoadLedger(dispatchLedgerPath())
	if err != nil {
		slog.Warn("dispatch: cannot read the usage ledger — daytime dispatch will be blocked", "error", err)
		return 0, false
	}

	pricing := usage.DefaultPricing().Merge(cfg.Budget.Pricing)
	return usage.SumWindow(turns, pricing, since, now) + ledger.SumSince(since), true
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
	win, inWindow := cfg.Schedule.InWindow(now)
	// The window gate is --auto only: an explicit `dispatch now` is the
	// operator asking for a tick, the same exemption the pause flag has.
	if dispatchAuto && !inWindow {
		fmt.Fprintln(cmd.OutOrStdout(), "outside dispatch window — nothing to do")
		return nil
	}

	ctx := context.Background()
	repos, err := fetchRepoInputs(ctx)
	if err != nil {
		return err
	}

	// The rung applies whether or not this is --auto: a manual daytime
	// dispatch burns the same quota.
	rungOn := inWindow && win.BudgetRung
	usedUSD, usedKnown := 0.0, true
	if rungOn {
		usedUSD, usedKnown = measureWindowUSD(ctx, cfg, now)
	}
	budget := dispatch.NewBudgetState(cfg.Budget, rungOn, usedUSD, usedKnown)

	openByRepo, globalOpen := countOpenAgentPRs(repos)
	decisions := dispatch.ApplyCaps(
		dispatch.Order(collectCandidates(repos), cfg.RepoPriority),
		cfg,
		dispatch.Counts{
			OpenPRsByRepo:     openByRepo,
			GlobalOpen:        globalOpen,
			DispatchedTonight: state.NightBudgetUsed(now),
		},
		budget,
	)

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
	return applyDecisions(ctx, decisions, state, now, cfg)
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

// countOpenAgentPRs counts open PRs that close one of the repo's own issues.
// Only those are pipeline output, so a hand-written PR never consumes a slot.
func countOpenAgentPRs(repos []repoInput) (map[string]int, int) {
	byRepo := make(map[string]int, len(repos))
	total := 0
	for _, r := range repos {
		for _, pr := range r.PRs {
			for _, i := range r.Issues {
				if dispatch.ClosesIssue(pr.Body, i.Number) {
					byRepo[r.Name]++
					total++
					break
				}
			}
		}
	}
	return byRepo, total
}

// applyDecisions writes the one label the dispatcher owns, then persists the
// nightly counter. It never writes agent:* or model:* — model choice belongs
// to agent-workflow's classify-task.sh.
func applyDecisions(ctx context.Context, ds []dispatch.Decision, state dispatch.State, now time.Time, cfg dispatch.Config) error {
	dispatched := 0
	for _, d := range ds {
		if !d.Dispatch {
			continue
		}
		gh, ok := clientFor("github").(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		owner, repo, num := d.Candidate.Owner, d.Candidate.Repo, d.Candidate.Issue.Number
		if _, err := gh.AddLabels(ctx, owner, repo, num, []string{dispatch.LabelAIImplement}); err != nil {
			return fmt.Errorf("label %s#%d: %w", repo, num, err)
		}
		if _, err := gh.CommentIssue(ctx, owner, repo, num,
			"Dispatched by `bridge dispatch`."); err != nil {
			return fmt.Errorf("comment %s#%d: %w", repo, num, err)
		}
		dispatched++
	}
	if dispatched > 0 {
		ledger, err := usage.LoadLedger(dispatchLedgerPath())
		if err != nil {
			return fmt.Errorf("read usage ledger: %w", err)
		}
		for _, d := range ds {
			if !d.Dispatch {
				continue
			}
			ledger.Append(usage.Run{
				At:     now,
				Repo:   d.Candidate.Repo,
				Issue:  d.Candidate.Issue.Number,
				EstUSD: cfg.Budget.MeanRunCostUSD,
			})
		}
		// Only the trailing window is ever summed; a week of history is
		// plenty for calibration and keeps the file bounded.
		ledger.Prune(now.AddDate(0, 0, -7))
		if err := usage.WriteLedger(dispatchLedgerPath(), ledger); err != nil {
			return fmt.Errorf("write usage ledger: %w", err)
		}
	}
	state.DispatchedTonight = state.NightBudgetUsed(now) + dispatched
	if state.NightBudgetUsed(now) == 0 {
		state.NightStartedAt = now
	}
	state.LastTick = now
	return dispatch.WriteState(dispatchStatePath(), state)
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
	win, inWindow := cfg.Schedule.InWindow(now)
	usedUSD, usedKnown := measureWindowUSD(context.Background(), cfg, now)
	budget := dispatch.NewBudgetState(cfg.Budget, inWindow && win.BudgetRung, usedUSD, usedKnown)

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
		}{
			Limits:            cfg.Limits,
			Paused:            state.Paused,
			DispatchedTonight: state.NightBudgetUsed(now),
			LastTick:          state.LastTick,
			BudgetWindowHours: cfg.Budget.WindowHours,
			BudgetUsedUSD:     budget.UsedUSD,
			BudgetLimitUSD:    budget.LimitUSD,
			BudgetKnown:       !budget.Unknown,
			BudgetRungActive:  budget.Enabled,
		})
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "paused: %t\n", state.Paused)
	fmt.Fprintf(w, "dispatched tonight: %d/%d\n", state.NightBudgetUsed(now), cfg.Limits.MaxDispatchesPerNight)
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
	fmt.Fprintf(w, "budget rung: %s\n", rungLabel(budget.Enabled, win, inWindow))
	if state.LastTick.IsZero() {
		fmt.Fprintln(w, "last tick: never")
	} else {
		fmt.Fprintf(w, "last tick: %s\n", state.LastTick.Format(time.RFC3339))
	}
	return nil
}

// rungLabel describes whether the budget rung is policing this moment, and
// which window that decision came from.
func rungLabel(enabled bool, win dispatch.Window, inWindow bool) string {
	if !inWindow {
		return "inactive (outside every configured window)"
	}
	if !enabled {
		return fmt.Sprintf("inactive (window %s-%s)", win.From, win.To)
	}
	return fmt.Sprintf("active (window %s-%s)", win.From, win.To)
}
