// cmd/bridge/mcp.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
	imcp "github.com/freaxnx01/bridge/internal/mcp"
	"github.com/freaxnx01/bridge/internal/overview"
	"github.com/freaxnx01/bridge/internal/remote"
)

var (
	mcpPort     int
	mcpHost     string
	mcpReadOnly bool
	mcpNoAuth   bool
)

func init() {
	rootCmd.AddCommand(newMCPCmd())
}

func newMCPCmd() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Bridge cross-forge MCP endpoint",
	}
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Bridge MCP server (Streamable HTTP)",
		RunE:  runMCPServe,
	}
	serveCmd.Flags().IntVar(&mcpPort, "port", 7788, "port to listen on")
	serveCmd.Flags().StringVar(&mcpHost, "host", "127.0.0.1", "host to bind to")
	serveCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "disable write tools (create_issue is not registered)")
	serveCmd.Flags().BoolVar(&mcpNoAuth, "no-auth", false, "skip bearer check (localhost dev only)")
	mcpCmd.AddCommand(serveCmd)
	return mcpCmd
}

// parseOwners parses a BRIDGE_MCP_OWNERS value: comma- and/or space-separated
// "forge:owner" entries. Malformed entries (no colon, empty parts) are skipped.
func parseOwners(s string) []imcp.Target {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	var out []imcp.Target
	for _, f := range fields {
		forgeName, owner, ok := strings.Cut(f, ":")
		if !ok || forgeName == "" || owner == "" {
			continue
		}
		out = append(out, imcp.Target{Forge: forgeName, Owner: owner})
	}
	return out
}

// buildMCPHandler mounts srv on a Streamable HTTP handler and, unless noAuth is
// set, wraps it in bearer-token middleware. It fails fast when a token is
// required but empty.
func buildMCPHandler(srv *sdkmcp.Server, token string, noAuth bool) (http.Handler, error) {
	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
	if noAuth {
		return streamable, nil
	}
	if token == "" {
		return nil, fmt.Errorf("BRIDGE_MCP_TOKEN is required (or pass --no-auth for localhost dev)")
	}
	middleware := sdkauth.RequireBearerToken(imcp.StaticBearerVerifier(token), nil)
	return middleware(streamable), nil
}

// validateNoAuthHost fails fast when --no-auth is combined with a
// non-loopback --host: skipping bearer auth is only safe when the server is
// unreachable from outside the machine.
func validateNoAuthHost(host string, noAuth bool) error {
	if !noAuth || isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("--no-auth requires a loopback --host (127.0.0.1, ::1, or localhost); got %q", host)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runMCPServe(cmd *cobra.Command, _ []string) error {
	if err := validateNoAuthHost(mcpHost, mcpNoAuth); err != nil {
		return err
	}

	roots := reposRoots()

	deps := imcp.Deps{
		ReadOnly:      mcpReadOnly || os.Getenv("BRIDGE_MCP_READONLY") == "1",
		DefaultOwners: parseOwners(os.Getenv("BRIDGE_MCP_OWNERS")),
		ClientFor:     newCachingClientResolver(clientForMCP(roots)),
		BuildOverview: buildOverviewSnapshot,
	}

	srv := imcp.NewServer(deps)
	handler, err := buildMCPHandler(srv, os.Getenv("BRIDGE_MCP_TOKEN"), mcpNoAuth)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", mcpHost, mcpPort)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout is intentionally 0: SSE connections are long-lived streams
		// and a write deadline would terminate them prematurely.
		IdleTimeout: 120 * time.Second,
	}

	slog.Info("Bridge MCP", "addr", "http://"+addr, "read_only", deps.ReadOnly, "auth", !mcpNoAuth)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		httpSrv.Shutdown(shutCtx) //nolint:errcheck // shutdown errors are not actionable at process exit
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// clientForMCP builds the Deps.ClientFor resolver: per-owner GitHub tokens and
// the single Forgejo token come from their direnv scopes via internal/remote.
func clientForMCP(roots []string) func(forgeName, owner string) forge.Client {
	return func(forgeName, owner string) forge.Client {
		switch forgeName {
		case "github":
			tok, ok := remote.GitHubToken(roots, owner)
			if !ok {
				return nil
			}
			return forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
		case "forgejo":
			tok, ok := remote.ForgejoToken(roots)
			if !ok {
				return nil
			}
			return forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
		}
		return nil
	}
}

// newCachingClientResolver wraps resolve (typically clientForMCP(roots)) with
// a resolve-once-per-(forge,owner) cache and adapts the returned forge.Client
// to imcp.ForgeReader. Token resolution walks the filesystem and spawns a
// direnv subprocess per call, so caching (including caching an unconfigured
// target's nil result) avoids paying that cost on every tool invocation for
// the life of the process.
func newCachingClientResolver(resolve func(forgeName, owner string) forge.Client) func(forgeName, owner string) imcp.ForgeReader {
	var (
		mu    sync.Mutex
		cache = map[string]imcp.ForgeReader{}
	)
	return func(forgeName, owner string) imcp.ForgeReader {
		key := forgeName + ":" + owner

		mu.Lock()
		if reader, ok := cache[key]; ok {
			mu.Unlock()
			return reader
		}
		mu.Unlock()

		var reader imcp.ForgeReader
		// imcp.ForgeReader's method set is a subset of forge.Client's, so a
		// non-nil client is assignable directly — no type assertion, and so no
		// path where a capable client silently degrades to nil. Assign only
		// when non-nil so an unconfigured target caches a nil interface.
		if c := resolve(forgeName, owner); c != nil {
			reader = c
		}

		mu.Lock()
		cache[key] = reader
		mu.Unlock()
		return reader
	}
}

// buildOverviewSnapshot mirrors serve.go's overview handler wiring.
func buildOverviewSnapshot(ctx context.Context) (overview.Snapshot, error) {
	repos := overviewRepos()
	return overview.Build(ctx, overview.Config{
		Environment:  os.Getenv("BRIDGE_ENV"),
		Repos:        repos,
		IdeasLabDir:  ideasLabDir(),
		FetchIssues:  func(c context.Context) ([]overview.Issue, error) { return fetchAllOpenIssues(c, repos) },
		FetchRoadmap: roadmapFetcher(),
	})
}
