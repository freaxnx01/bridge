//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/launcher"
)

var (
	launchWorktree string
	launchAgent    string
	launchRC       bool
	launchRCWait   time.Duration
	launchNoSync   bool
	launchJSON     bool
)

// launchResult is `bridge launch --json` output.
type launchResult struct {
	Slot            string `json:"slot"`
	Repo            string `json:"repo"`
	Worktree        string `json:"worktree,omitempty"`
	Dir             string `json:"dir"`
	Agent           string `json:"agent"`
	WorktreeCreated bool   `json:"worktree_created"`
	AlreadyRunning  bool   `json:"already_running"`
	RCURL           string `json:"rc_url,omitempty"`
}

var launchCmd = &cobra.Command{
	Use:   "launch <repo>",
	Short: "Start an agent session in the background (no attach) — for scripts and the Telegram admin session",
	Long: `launch resolves <repo> (and -w worktree) exactly like ` + "`bridge <repo>`" + `, but
creates the tmux session detached and returns instead of attaching. Idempotent:
a live slot is reported as already_running and left untouched. With --rc the
Remote Control URL is scraped from the pane (best effort, up to --rc-wait).`,
	Args:              cobra.ExactArgs(1),
	RunE:              runLaunch,
	ValidArgsFunction: completeRepoName,
}

func init() {
	launchCmd.Flags().StringVarP(&launchWorktree, "worktree", "w", "", "worktree name (created under .worktrees/ if missing)")
	launchCmd.Flags().StringVar(&launchAgent, "agent", "", "agent (claude|copilot|opencode|code); default BRIDGE_DEFAULT_AGENT")
	launchCmd.Flags().BoolVar(&launchRC, "rc", false, "enable Remote Control (claude only) and report its URL")
	launchCmd.Flags().DurationVar(&launchRCWait, "rc-wait", 20*time.Second, "how long to wait for the Remote Control URL")
	launchCmd.Flags().BoolVar(&launchNoSync, "no-sync", false, "skip the pre-launch git pull")
	launchCmd.Flags().BoolVar(&launchJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(launchCmd)
}

// rcURLPattern matches the Remote Control link claude prints in its pane.
var rcURLPattern = regexp.MustCompile(`https://claude\.ai/code/[A-Za-z0-9_\-/?=&.]+`)

func runLaunch(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	fail := func(format string, a ...any) error {
		fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
		os.Exit(2)
		return nil
	}
	t, err := resolveLaunchTarget(args[0], launchWorktree, launchAgent)
	if err != nil {
		var repoErr errRepoLookup
		var agentErr errAgentLookup
		if errors.As(err, &repoErr) || errors.As(err, &agentErr) {
			return fail("%v", err)
		}
		return err
	}
	if !t.HasAgent {
		return fail("bridge: no agent — pass --agent or set BRIDGE_DEFAULT_AGENT")
	}
	if launchRC {
		if t.Spec.Name != "claude" {
			return fail("bridge: --rc needs the claude agent (got %s)", t.Spec.Name)
		}
		if !slices.Contains(t.Spec.Args, "--remote-control") {
			t.Spec.Args = append(slices.Clone(t.Spec.Args), "--remote-control")
		}
	}

	running := sessionLive(t.Slot)
	if !running {
		finalizeLaunch(&t, launchNoSync)
		argv, err := launcher.Tmux{}.LaunchArgvDetached(t.Slot, t.WorkDir, t.Spec)
		if err != nil {
			return err
		}
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux: %v: %s", err, out)
		}
	}

	res := launchResult{
		Slot: t.Slot, Repo: t.Repo.Name, Worktree: t.Worktree, Dir: t.WorkDir,
		Agent: t.AgentName, WorktreeCreated: t.WorktreeCreated, AlreadyRunning: running,
	}
	if launchRC {
		res.RCURL = waitRCURL(t.Slot, launchRCWait)
		if res.RCURL == "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "bridge: no Remote Control URL seen within %s (session is running)\n", launchRCWait)
		}
	}
	if launchJSON {
		return emitJSON(cmd.OutOrStdout(), res)
	}
	state := "launched"
	if running {
		state = "already running"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", state, t.Slot, t.WorkDir)
	if res.RCURL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "remote control: %s\n", res.RCURL)
	}
	return nil
}

// waitRCURL polls the slot's pane for the Remote Control link. Best effort:
// returns "" on timeout or any tmux error.
func waitRCURL(slot string, wait time.Duration) string {
	deadline := time.Now().Add(wait)
	for {
		out, err := exec.Command("tmux", "capture-pane", "-p", "-J", "-t", slot, "-S", "-200").Output()
		if err == nil {
			if m := rcURLPattern.FindString(string(out)); m != "" {
				return m
			}
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(500 * time.Millisecond)
	}
}
