// cmd/bridge/pipeline.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// errNoForgeClient means no token resolves for a repo's forge and owner. The
// collector records it as a source error, so a missing token shows on the
// board instead of reading as "no issues".
var errNoForgeClient = errors.New("no forge client")

// clientResolver returns the forge client for one forge and owner, or nil when
// no token is configured for it.
type clientResolver func(forgeName, owner string) forge.Client

// cachedClients resolves each (forge, owner) once: token resolution walks the
// repos tree and spawns direnv, too slow to repeat on every collect.
func cachedClients(resolve clientResolver) clientResolver {
	var (
		mu    sync.Mutex
		cache = map[string]forge.Client{}
	)
	return func(forgeName, owner string) forge.Client {
		key := forgeName + ":" + owner
		mu.Lock()
		defer mu.Unlock()
		if c, ok := cache[key]; ok {
			return c
		}
		c := resolve(forgeName, owner)
		cache[key] = c
		return c
	}
}

// repoClient resolves r's client, or errNoForgeClient when none is configured.
func repoClient(resolve clientResolver, r core.Repo) (forge.Client, error) {
	if c := resolve(r.Forge, r.Owner); c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("%w: %s owner %s", errNoForgeClient, r.Forge, r.Owner)
}

// githubOnly resolves r's GitHub client. Other forges have no PRs, milestones
// or runs on the board, so they yield nil without an error.
func githubOnly(resolve clientResolver, r core.Repo) (*forge.GithubClient, error) {
	if r.Forge != "github" {
		return nil, nil
	}
	c, err := repoClient(resolve, r)
	if err != nil {
		return nil, err
	}
	gh, ok := c.(*forge.GithubClient)
	if !ok {
		return nil, fmt.Errorf("%w: github client has type %T", errNoForgeClient, c)
	}
	return gh, nil
}

// boardForge reports whether the cockpit has a client for forgeName. It
// mirrors clientForMCP's switch.
func boardForge(name string) bool { return name == "github" || name == "forgejo" }

// runsPerRepo is how many recent workflow runs the board reads per repo.
const runsPerRepo = 30

// cockpitSources wires the collector to the real forges, sessions and dispatch
// state. Clients are resolved per owner, like the MCP server does, because the
// service gets its tokens from each forge directory's .envrc, not GH_TOKEN.
func cockpitSources(resolve clientResolver) cockpit.Sources {
	return cockpit.Sources{
		Repos: reposWithMeta,
		Wired: repoWired,
		Issues: func(ctx context.Context, r core.Repo) ([]forge.Issue, error) {
			if !boardForge(r.Forge) {
				return nil, fmt.Errorf("%w: %s", cockpit.ErrForgeUnsupported, r.Forge)
			}
			cl, err := repoClient(resolve, r)
			if err != nil {
				return nil, err
			}
			return cl.ListOpenIssues(ctx, r.Owner, r.Name)
		},
		PRs: func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error) {
			gh, err := githubOnly(resolve, r)
			if gh == nil {
				return nil, err
			}
			return gh.ListOpenPullRequests(ctx, r.Owner, r.Name)
		},
		Milestones: func(ctx context.Context, r core.Repo) ([]forge.Milestone, error) {
			gh, err := githubOnly(resolve, r)
			if gh == nil {
				return nil, err
			}
			return gh.ListOpenMilestones(ctx, r.Owner, r.Name)
		},
		Runs: func(ctx context.Context, r core.Repo) ([]forge.WorkflowRun, error) {
			gh, err := githubOnly(resolve, r)
			if gh == nil {
				return nil, err
			}
			return gh.ListWorkflowRuns(ctx, r.Owner, r.Name, "", "", runsPerRepo)
		},
		Issue: func(ctx context.Context, r core.Repo, number int) (forge.Issue, []forge.Comment, error) {
			gh, err := githubOnly(resolve, r)
			if err != nil {
				return forge.Issue{}, nil, err
			}
			if gh == nil {
				// Only GitHub posts run markers, so the inbox never asks for
				// another forge's comments; saying so beats a nil, nil answer.
				return forge.Issue{}, nil, fmt.Errorf("%w: %s is not github", errNoForgeClient, r.Forge)
			}
			return gh.GetIssue(ctx, r.Owner, r.Name, number)
		},
		Sessions: func(ctx context.Context) ([]agentview.Session, error) {
			return agentview.List(ctx, agentview.ExecRunner{})
		},
		Paused: func() bool {
			state, err := dispatch.ReadState(dispatchStatePath())
			return err == nil && state.Paused
		},
		Preview: previewTick,
	}
}

// previewTick evaluates what the next acting dispatch tick would decide, with
// no side effect of any kind: config and state are re-read each call (the
// operator edits them while serve runs), and nothing is written back.
//
// The retry pass is left out on purpose — it costs a workflow-run listing and
// a comment read per candidate, which an open board endpoint must not spend on
// a 10-minute timer. Retries lead the real tick's order, so the preview is the
// dispatch pass alone.
func previewTick(ctx context.Context, repos []dispatch.RepoInput) (dispatch.Tick, error) {
	cfg, err := dispatch.LoadConfig(dispatchConfigPath())
	if err != nil {
		return dispatch.Tick{}, fmt.Errorf("dispatch config: %w", err)
	}
	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		return dispatch.Tick{}, fmt.Errorf("dispatch state: %w", err)
	}

	now := time.Now()
	gate := resolveGate(ctx, repos, cfg.Lanes, now)
	usedUSD, usedKnown := previewBudget(ctx, cfg, dispatch.NextActingInstant(cfg, now), now)
	return dispatch.Evaluate(dispatch.TickInput{
		Repos: repos, Config: cfg, State: state, Gate: gate,
		UsedUSD: usedUSD, UsedKnown: usedKnown, Now: now, Mode: dispatch.GateNextWindow,
	}), nil
}

// previewBudget measures the window the previewed tick would be guarded by.
// Usage can only be read up to now, so a rung guarding a window that has not
// started yet falls back to the plain trailing window — the same reading
// `dispatch status` gives outside a guarded moment.
func previewBudget(ctx context.Context, cfg dispatch.Config, at, now time.Time) (float64, bool) {
	timing := dispatch.ResolveTiming(cfg, at)
	if !timing.RungOn {
		return 0, true
	}
	from := timing.MeasureFrom
	if from.After(now) {
		from = now.Add(-time.Duration(cfg.Budget.WindowHours * float64(time.Hour)))
	}
	return measureWindowUSD(ctx, cfg, from, now)
}

// pipelineHandler builds the Pipeline API on top of a running collector. ctx is
// the server's: background refreshes outlive the request that triggered them.
func pipelineHandler(ctx context.Context, col *cockpit.Collector, repos func() ([]core.Repo, error),
	resolve clientResolver) *api.PipelineHandler {
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
	launch := func(_ context.Context, req api.LaunchRequest, worktreePrefix, prompt string) (api.LaunchResult, error) {
		// The child runs on the server ctx: a client disconnect must not kill a half-started launch.
		res, err := launchSession(ctx, repos, req, worktreePrefix, prompt)
		if err != nil {
			slog.Error("pipeline launch failed", "repo", req.Repo, "number", req.Number, "error", err)
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
		ClearLock: func(c context.Context, req api.LaunchRequest) error {
			if err := clearEnrichmentLock(c, resolve, repos, time.Now, req); err != nil {
				slog.Error("clear enrichment lock failed", "repo", req.Repo, "number", req.Number, "error", err)
				return err
			}
			owner, name, _ := strings.Cut(req.Repo, "/")
			// On the server ctx, like the launch path: the refresh must outlive the request.
			go col.CollectRepo(ctx, owner, name)
			return nil
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
		return "", fmt.Errorf("%w: %s", api.ErrUnknownRepo, repo)
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

// requireTokenConfigured fails closed: the pipeline write endpoints start paid,
// unattended sessions, so without a BRIDGE_API_TOKEN they refuse to run at all.
func requireTokenConfigured(token string, next http.Handler) http.Handler {
	if token != "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte(`{"error":"BRIDGE_API_TOKEN not set"}` + "\n")); err != nil {
			// The client went away mid-response; nothing else to send, so just record it.
			slog.Warn("pipeline: write token-not-set response", "error", err)
		}
	})
}
