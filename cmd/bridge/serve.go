// cmd/bridge/serve.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

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
		Discover: func() ([]core.Repo, error) { return discoverAllRoots() },
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
		Idea: func(c context.Context, target, text string) (string, error) {
			repos, _ := discoverAllRoots()
			tgt, err := resolveCaptureTarget(target, os.Getenv("BRIDGE_IDEAS_LAB_REPO"), repos)
			if err != nil {
				return "", err
			}
			tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
			if !ok {
				return "", fmt.Errorf("no github token for owner %q", tgt.Owner)
			}
			return capture.CaptureIdea(c, forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API")), tgt, text, time.Now())
		},
		Issue: func(c context.Context, owner, repo, title string) (forge.Issue, error) {
			repos, _ := discoverAllRoots()
			tgt, err := resolveIssueTarget(owner+"/"+repo, repos)
			if err != nil {
				return forge.Issue{}, err
			}
			var creator capture.IssueCreator
			switch tgt.Forge {
			case "github":
				tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
				if !ok {
					return forge.Issue{}, fmt.Errorf("no github token for owner %q", tgt.Owner)
				}
				creator = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
			case "forgejo":
				tok, ok := remote.ForgejoToken(reposRoots())
				if !ok {
					return forge.Issue{}, fmt.Errorf("no forgejo token")
				}
				creator = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
			default:
				return forge.Issue{}, fmt.Errorf("forge %q not supported for issue capture", tgt.Forge)
			}
			return capture.CaptureIssue(c, creator, tgt.Owner, tgt.Repo, title)
		},
		Notify: notify,
	}

	apiMux := http.NewServeMux()
	apiMux.Handle("/api/overview", overviewH)
	apiMux.Handle("/api/repos/", reposH)
	apiMux.Handle("/api/repos", reposH)
	apiMux.Handle("/api/capture/", captureH)

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
