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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/freaxnx01/bridge/internal/audit"
	"github.com/freaxnx01/bridge/internal/forge"
	imcp "github.com/freaxnx01/bridge/internal/mcp"
	"github.com/freaxnx01/bridge/internal/oauth"
	"github.com/freaxnx01/bridge/internal/overview"
	"github.com/freaxnx01/bridge/internal/remote"
)

var (
	mcpPort             int
	mcpHost             string
	mcpReadOnly         bool
	mcpAllowDestructive bool
	mcpNoAuth           bool
	mcpAuthMode         string
	mcpPutFileAllowlist string
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
	serveCmd.Flags().BoolVar(&mcpAllowDestructive, "allow-destructive", false, "allow destructive tools to execute when confirmed (reserved for future archive_repo/delete_repo; tier-1 tools are unaffected)")
	serveCmd.Flags().BoolVar(&mcpNoAuth, "no-auth", false, "skip bearer check (localhost dev only)")
	serveCmd.Flags().StringVar(&mcpAuthMode, "auth", "static", "auth mode: static (bearer token) or oauth")
	serveCmd.Flags().StringVar(&mcpPutFileAllowlist, "put-file-allowlist", "docs/**/*.md,*.md", "comma-separated path patterns put_file may write to (each entry is \"dir/**\" or a root-level \"*.ext\" glob); .github/** is always denied")
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

// parseAllowedRedirectURIs parses BRIDGE_MCP_ALLOWED_REDIRECT_URIS: a
// comma-separated list of absolute redirect URIs bridge will accept at
// /oauth/register and /oauth/authorize. Entries are trimmed of surrounding
// whitespace, since a comma-separated value in a systemd unit file very often
// has spaces after commas; Config.Validate rejects any entry that is empty
// or not an absolute URL.
func parseAllowedRedirectURIs(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// parsePathAllowlist parses a --put-file-allowlist / BRIDGE_MCP_PUT_FILE_ALLOWLIST
// value: comma-separated patterns, trimmed of surrounding whitespace, empty
// entries skipped. An empty input falls back to imcp.DefaultPathAllowlist.
func parsePathAllowlist(s string) imcp.PathAllowlist {
	if strings.TrimSpace(s) == "" {
		return imcp.DefaultPathAllowlist
	}
	parts := strings.Split(s, ",")
	out := make(imcp.PathAllowlist, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return imcp.DefaultPathAllowlist
	}
	return out
}

// firstNonEmpty returns the first non-empty string, or "" if both are empty.
// Used to let an env var override a flag's value only when the env var is
// actually set (unlike the flag-OR-env boolean pattern used for
// AllowDestructive, a full allowlist replacement needs "env wins if set",
// not "either source enables it").
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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

// buildOAuthHandler mounts the MCP transport behind OAuth bearer auth and the
// authorization-server endpoints beside it. The OAuth and metadata routes sit
// outside the bearer middleware: a client cannot present a token it has not yet
// obtained. Returns a close func releasing the state-directory lock.
func buildOAuthHandler(srv *sdkmcp.Server, cfg oauth.Config, discoveryClient *http.Client) (http.Handler, func() error, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	store, err := oauth.OpenStore(cfg.StateDir)
	if err != nil {
		return nil, nil, err
	}

	authServer := oauth.NewServer(cfg, store)

	// Discovery is best-effort at startup: an authentik outage must not stop
	// bridge from serving already-issued tokens.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if eps, err := oauth.DiscoverAuthentik(ctx, cfg.AuthentikIssuer, discoveryClient); err != nil {
		slog.Warn("authentik discovery failed; new logins will be unavailable until it recovers", "err", err)
	} else {
		authServer.SetAuthentik(eps)
	}

	resourceMeta := sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             cfg.Issuer,
		AuthorizationServers: []string{cfg.Issuer},
	})

	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
	guarded := sdkauth.RequireBearerToken(store.Verifier(), &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: strings.TrimRight(cfg.Issuer, "/") + "/.well-known/oauth-protected-resource",
	})(streamable)

	mux := http.NewServeMux()
	mux.Handle("/.well-known/oauth-protected-resource", resourceMeta)
	mux.Handle("/.well-known/oauth-authorization-server", authServer.Handler())
	mux.Handle("/oauth/", authServer.Handler())
	mux.Handle("/", guarded)

	return mux, store.Close, nil
}

// mcpStateDir resolves the OAuth state directory: the env override wins,
// otherwise ~/.local/state/bridge-mcp. A home-resolution failure is returned
// as an error rather than surfacing downstream as a misleading "missing
// BRIDGE_MCP_STATE_DIR" diagnostic.
func mcpStateDir() (string, error) {
	if dir := os.Getenv("BRIDGE_MCP_STATE_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for default OAuth state dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "bridge-mcp"), nil
}

// auditLogPath resolves the audit log path: BRIDGE_AUDIT_LOG_PATH wins,
// then $XDG_STATE_HOME/bridge/audit.jsonl, else ~/.local/state/bridge/audit.jsonl.
func auditLogPath() (string, error) {
	if p := os.Getenv("BRIDGE_AUDIT_LOG_PATH"); p != "" {
		return p, nil
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "bridge", "audit.jsonl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for default audit log path: %w", err)
	}
	return filepath.Join(home, ".local", "state", "bridge", "audit.jsonl"), nil
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
	if mcpNoAuth && mcpAuthMode == "oauth" {
		return fmt.Errorf("--no-auth is incompatible with --auth=oauth: OAuth mode always requires a bearer token")
	}
	if err := validateNoAuthHost(mcpHost, mcpNoAuth); err != nil {
		return err
	}

	roots := reposRoots()

	logPath, err := auditLogPath()
	if err != nil {
		return err
	}
	auditLogger, err := audit.Open(logPath)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}

	deps := imcp.Deps{
		ReadOnly:         mcpReadOnly || os.Getenv("BRIDGE_MCP_READONLY") == "1",
		AllowDestructive: mcpAllowDestructive || os.Getenv("BRIDGE_MCP_ALLOW_DESTRUCTIVE") == "1",
		DefaultOwners:    parseOwners(os.Getenv("BRIDGE_MCP_OWNERS")),
		PathAllowlist:    parsePathAllowlist(firstNonEmpty(os.Getenv("BRIDGE_MCP_PUT_FILE_ALLOWLIST"), mcpPutFileAllowlist)),
		ClientFor:        newCachingClientResolver(clientForMCP(roots)),
		BuildOverview:    buildOverviewSnapshot,
		Audit:            auditLogger,
	}

	srv := imcp.NewServer(deps)

	var (
		handler http.Handler
		cleanup = func() error { return nil }
	)
	switch mcpAuthMode {
	case "static":
		handler, err = buildMCPHandler(srv, os.Getenv("BRIDGE_MCP_TOKEN"), mcpNoAuth)
	case "oauth":
		var stateDir string
		stateDir, err = mcpStateDir()
		if err != nil {
			return err
		}
		handler, cleanup, err = buildOAuthHandler(srv, oauth.Config{
			Issuer:              os.Getenv("BRIDGE_MCP_ISSUER"),
			AuthentikIssuer:     os.Getenv("BRIDGE_OIDC_ISSUER"),
			ClientID:            os.Getenv("BRIDGE_OIDC_CLIENT_ID"),
			ClientSecret:        os.Getenv("BRIDGE_OIDC_CLIENT_SECRET"),
			AllowedSubject:      os.Getenv("BRIDGE_OIDC_ALLOWED_SUB"),
			StateDir:            stateDir,
			AllowedRedirectURIs: parseAllowedRedirectURIs(os.Getenv("BRIDGE_MCP_ALLOWED_REDIRECT_URIS")),
		}, http.DefaultClient)
	default:
		return fmt.Errorf("--auth must be static or oauth, got %q", mcpAuthMode)
	}
	if err != nil {
		return err
	}
	// The store holds an OS-level single-instance lock; release it on every
	// exit path so a restart doesn't find the state directory still locked.
	defer func() { _ = cleanup() }() // best-effort release; process is exiting regardless

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

	slog.Info("Bridge MCP", "addr", "http://"+addr, "read_only", deps.ReadOnly, "allow_destructive", deps.AllowDestructive, "auth", !mcpNoAuth, "auth_mode", mcpAuthMode)

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
