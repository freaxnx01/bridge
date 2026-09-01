// cmd/bridge/serve.go
package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/capture"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
	"github.com/freaxnx01/bridge/internal/remote"
	"github.com/freaxnx01/bridge/internal/web"
)

var servePort int
var serveHost string

func init() {
	rootCmd.AddCommand(newServeCmd())
}

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Bridge WebUI HTTP server",
		RunE:  runServe,
	}
	cmd.Flags().IntVar(&servePort, "port", 7777, "port to listen on")
	cmd.Flags().StringVar(&serveHost, "host", "127.0.0.1", "host to bind to")
	return cmd
}

// requireBearer gates a handler behind a static bearer token. When token is
// empty, auth is disabled and next is returned unchanged (dev/LAN default). When
// set, requests must carry "Authorization: Bearer <token>" (constant-time
// compared) or receive 401.
func requireBearer(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func runServe(cmd *cobra.Command, _ []string) error {
	hub := web.NewHub()
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	go hub.Run(ctx)

	notify := func(eventType string, data any) {
		hub.Broadcast(web.Event{Type: eventType, Data: data})
	}

	overviewH := &api.OverviewHandler{
		Build: func(c context.Context) (overview.Snapshot, error) {
			repos := overviewRepos()
			return overview.Build(c, overview.Config{
				Environment:  os.Getenv("BRIDGE_ENV"),
				Repos:        repos,
				IdeasLabDir:  ideasLabDir(),
				FetchIssues:  func(c context.Context) ([]overview.Issue, error) { return fetchAllOpenIssues(c, repos) },
				FetchRoadmap: roadmapFetcher(),
			})
		},
	}

	reposH := &api.ReposHandler{
		// reposWithMeta, not discoverAllRoots: discovery alone leaves Desc and Topics
		// empty, because those live in the repo-meta.json cache rather than on disk in
		// the clone. GET /api/repos is what FlowHub's Skills:Bridge catalogue reads, and
		// its repo inference matches on name + description — served un-enriched, every
		// description is blank and inference silently degrades to name-only matching.
		Discover: reposWithMeta,
		Issues: func(c context.Context, forgeName, owner, repo string) ([]forge.Issue, error) {
			cl := clientFor(forgeName)
			if cl == nil {
				return nil, nil
			}
			return cl.ListOpenIssues(c, owner, repo)
		},
		Create: func(c context.Context, name, forgeName string, private bool) (core.Repo, error) {
			repo, _, err := createAndClone(c, name, forgeName, private)
			return repo, err
		},
		Notify: notify,
	}

	captureH := &api.CaptureHandler{
		Idea: func(c context.Context, p api.IdeaParams) (string, error) {
			repos, _ := reposWithMeta()
			var tgt capture.Target
			if p.Alias != "" {
				r, err := core.ResolveAlias(p.Alias, repos)
				if err != nil {
					return "", err // ErrAliasNotFound / ErrAliasAmbiguous → mapped by the handler
				}
				tgt = capture.Target{Owner: r.Owner, Repo: r.Name}
			} else {
				resolved, err := resolveCaptureTarget(p.Target, os.Getenv("BRIDGE_IDEAS_LAB_REPO"), repos)
				if err != nil {
					return "", err
				}
				tgt = resolved
			}
			tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
			if !ok {
				return "", fmt.Errorf("no github token for owner %q", tgt.Owner)
			}
			return capture.CaptureIdea(c, forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API")), tgt, p.Text, time.Now())
		},
		Issue: func(c context.Context, p api.IssueParams) (forge.Issue, error) {
			repos, _ := reposWithMeta()
			var owner, repo, forgeName string
			if p.Alias != "" {
				r, err := core.ResolveAlias(p.Alias, repos)
				if err != nil {
					return forge.Issue{}, err // ErrAliasNotFound / ErrAliasAmbiguous → mapped by the handler
				}
				owner, repo, forgeName = r.Owner, r.Name, r.Forge
			} else {
				tgt, err := resolveIssueTarget(p.Owner+"/"+p.Repo, repos)
				if err != nil {
					return forge.Issue{}, err
				}
				owner, repo, forgeName = tgt.Owner, tgt.Repo, tgt.Forge
			}
			creator, err := issueCreatorFor(forgeName, owner)
			if err != nil {
				return forge.Issue{}, err
			}
			return capture.CaptureIssue(c, creator, owner, repo, p.Title, p.Body)
		},
		Notify: notify,
	}

	agentsH := &api.AgentsHandler{
		List: func(ctx context.Context) ([]agentview.Session, error) {
			return agentview.List(ctx, agentview.ExecRunner{})
		},
	}

	apiMux := http.NewServeMux()
	apiMux.Handle("/api/overview", overviewH)
	apiMux.Handle("/api/repos/", reposH)
	apiMux.Handle("/api/repos", reposH)
	apiToken := os.Getenv("BRIDGE_API_TOKEN")
	apiMux.Handle("/api/capture/", requireBearer(apiToken, captureH))
	apiMux.Handle("/api/agents", agentsH)

	// Broadcast overview-updated every 10s so connected clients stay live.
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				hub.Broadcast(web.Event{Type: "overview-updated"})
				hub.Broadcast(web.Event{Type: "agents-updated"})
			}
		}
	}()

	addr := fmt.Sprintf("%s:%d", serveHost, servePort)
	srv := &http.Server{
		Addr:    addr,
		Handler: web.NewServer(hub, apiMux).Handler(),
		// WriteTimeout is intentionally 0: SSE connections are long-lived streams
		// and a write deadline would terminate them prematurely.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("Bridge WebUI", "addr", "http://"+addr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx) //nolint:errcheck // shutdown errors (e.g. context deadline exceeded) are not actionable at process exit
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
