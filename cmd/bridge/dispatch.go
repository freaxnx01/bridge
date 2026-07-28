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

	repos, err := fetchRepoInputs(context.Background())
	if err != nil {
		return err
	}

	openByRepo, globalOpen := countOpenAgentPRs(repos)
	now := time.Now()
	decisions := dispatch.ApplyCaps(
		dispatch.Order(collectCandidates(repos)),
		cfg, openByRepo, globalOpen, state.NightBudgetUsed(now),
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
	return applyDecisions(context.Background(), decisions, state, now)
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
func applyDecisions(ctx context.Context, ds []dispatch.Decision, state dispatch.State, now time.Time) error {
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
// It reads no network state: in-flight PR counts would require a repo fetch,
// which is out of scope for a v1 status command — this stays as cheap and
// side-effect-free as pause/resume.
func runDispatchStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := dispatch.LoadConfig(dispatchConfigPath())
	if err != nil {
		return err
	}
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		return err
	}

	if dispatchJSON {
		return emitJSON(cmd.OutOrStdout(), struct {
			Limits            dispatch.Limits `json:"limits"`
			Paused            bool            `json:"paused"`
			DispatchedTonight int             `json:"dispatched_tonight"`
			LastTick          time.Time       `json:"last_tick,omitempty"`
		}{
			Limits:            cfg.Limits,
			Paused:            state.Paused,
			DispatchedTonight: state.NightBudgetUsed(time.Now()),
			LastTick:          state.LastTick,
		})
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "paused: %t\n", state.Paused)
	fmt.Fprintf(w, "dispatched tonight: %d/%d\n", state.NightBudgetUsed(time.Now()), cfg.Limits.MaxDispatchesPerNight)
	fmt.Fprintf(w, "per-repo cap: %d, global cap: %d\n", cfg.Limits.PerRepo, cfg.Limits.GlobalOpenPRs)
	if state.LastTick.IsZero() {
		fmt.Fprintln(w, "last tick: never")
	} else {
		fmt.Fprintf(w, "last tick: %s\n", state.LastTick.Format(time.RFC3339))
	}
	return nil
}
