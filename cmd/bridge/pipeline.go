// cmd/bridge/pipeline.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// launchTimeout bounds one `bridge launch` child: --rc-wait plus session startup.
const launchTimeout = 60 * time.Second

// wiredWorkflows are the files whose presence marks a repo as wired to the agent pipeline.
var wiredWorkflows = []string{"agent.yml", "agent-implement.yml"}

// repoWired reports whether the clone carries an agent workflow.
func repoWired(r core.Repo) bool {
	for _, f := range wiredWorkflows {
		if _, err := os.Stat(filepath.Join(r.Path, ".github", "workflows", f)); err == nil {
			return true
		}
	}
	return false
}

// githubOnly resolves the GitHub client of r, or nil for any other forge.
func githubOnly(r core.Repo) *forge.GithubClient {
	if r.Forge != "github" {
		return nil
	}
	gh, ok := clientFor(r.Forge).(*forge.GithubClient)
	if !ok {
		return nil
	}
	return gh
}

// runsPerRepo is how many recent workflow runs the board reads per repo.
const runsPerRepo = 30

// cockpitSources wires the collector to the real forges, sessions and dispatch state.
func cockpitSources() cockpit.Sources {
	return cockpit.Sources{
		Repos: reposWithMeta,
		Wired: repoWired,
		Issues: func(ctx context.Context, r core.Repo) ([]forge.Issue, error) {
			cl := clientFor(r.Forge)
			if cl == nil {
				return nil, nil
			}
			return cl.ListOpenIssues(ctx, r.Owner, r.Name)
		},
		PRs: func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error) {
			gh := githubOnly(r)
			if gh == nil {
				return nil, nil
			}
			return gh.ListOpenPullRequests(ctx, r.Owner, r.Name)
		},
		Milestones: func(ctx context.Context, r core.Repo) ([]forge.Milestone, error) {
			gh := githubOnly(r)
			if gh == nil {
				return nil, nil
			}
			return gh.ListOpenMilestones(ctx, r.Owner, r.Name)
		},
		Runs: func(ctx context.Context, r core.Repo) ([]forge.WorkflowRun, error) {
			gh := githubOnly(r)
			if gh == nil {
				return nil, nil
			}
			return gh.ListWorkflowRuns(ctx, r.Owner, r.Name, "", "", runsPerRepo)
		},
		Sessions: func(ctx context.Context) ([]agentview.Session, error) {
			return agentview.List(ctx, agentview.ExecRunner{})
		},
		Paused: func() bool {
			state, err := dispatch.ReadState(dispatchStatePath())
			return err == nil && state.Paused
		},
	}
}

// pipelineHandler builds the Pipeline API on top of a running collector. ctx is
// the server's: background refreshes outlive the request that triggered them.
func pipelineHandler(ctx context.Context, col *cockpit.Collector, repos func() ([]core.Repo, error)) *api.PipelineHandler {
	var refreshing atomic.Bool
	refresh := func(context.Context) {
		if !refreshing.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer refreshing.Store(false)
			col.CollectIssues(ctx)
			col.CollectRuns(ctx)
			col.CollectSessions(ctx)
		}()
	}
	launch := func(c context.Context, req api.LaunchRequest, worktreePrefix, prompt string) (api.LaunchResult, error) {
		res, err := launchSession(c, repos, req, worktreePrefix, prompt)
		if err != nil {
			return res, err
		}
		owner, name, _ := strings.Cut(req.Repo, "/")
		go func() {
			col.CollectSessions(ctx)
			col.CollectRepo(ctx, owner, name)
		}()
		return res, nil
	}
	return &api.PipelineHandler{
		Board:   col.Board,
		Refresh: refresh,
		Enrich: func(c context.Context, req api.LaunchRequest) (api.LaunchResult, error) {
			return launch(c, req, "enrich", fmt.Sprintf("/enrich %d --headless", req.Number))
		},
		Dispatch: func(c context.Context, req api.LaunchRequest) (api.LaunchResult, error) {
			return launch(c, req, "dispatch", fmt.Sprintf("/gh:implement %d", req.Number))
		},
	}
}

// launchArgs is the `bridge launch` argv for one action session.
func launchArgs(repoName, worktreePrefix string, number int, prompt string) []string {
	return []string{
		"launch", repoName,
		"-w", fmt.Sprintf("%s-%d", worktreePrefix, number),
		"--agent", "claude", "--rc", "--rc-wait", "15s", "--json",
		"--prompt", prompt,
	}
}

// resolveLaunchName returns the basename `bridge launch` must be given, or
// api.ErrAmbiguousRepo when another repo shares it (launch resolves by basename).
func resolveLaunchName(repos []core.Repo, repo string) (string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var matches []core.Repo
	for _, r := range repos {
		if r.Name == name {
			matches = append(matches, r)
		}
	}
	switch {
	case len(matches) > 1:
		return "", api.ErrAmbiguousRepo
	case len(matches) == 0 || matches[0].Owner != owner:
		return "", fmt.Errorf("unknown repo %s", repo)
	}
	return name, nil
}

// launchSession runs `bridge launch` as a child process and parses its --json.
func launchSession(ctx context.Context, repos func() ([]core.Repo, error), req api.LaunchRequest, worktreePrefix, prompt string) (api.LaunchResult, error) {
	all, err := repos()
	if err != nil {
		return api.LaunchResult{}, fmt.Errorf("list repos: %w", err)
	}
	name, err := resolveLaunchName(all, req.Repo)
	if err != nil {
		return api.LaunchResult{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return api.LaunchResult{}, fmt.Errorf("locate bridge binary: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, launchArgs(name, worktreePrefix, req.Number, prompt)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return api.LaunchResult{}, fmt.Errorf("bridge launch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseLaunchResult(out)
}

// parseLaunchResult maps `bridge launch --json` (snake_case) onto the API shape.
func parseLaunchResult(out []byte) (api.LaunchResult, error) {
	var lr struct {
		Slot           string `json:"slot"`
		AlreadyRunning bool   `json:"already_running"`
		RCURL          string `json:"rc_url"`
	}
	if err := json.Unmarshal(out, &lr); err != nil {
		return api.LaunchResult{}, fmt.Errorf("parse launch output: %w", err)
	}
	if lr.Slot == "" {
		return api.LaunchResult{}, errors.New("launch output has no slot")
	}
	return api.LaunchResult{Slot: lr.Slot, AlreadyRunning: lr.AlreadyRunning, RCURL: lr.RCURL}, nil
}
