package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
	imcp "github.com/freaxnx01/bridge/internal/mcp"
	imcpoauth "github.com/freaxnx01/bridge/internal/oauth"
)

// fakeResolvedClient is a minimal forge.Client/imcp.ForgeReader double used to
// verify newCachingClientResolver's caching behavior without touching real
// tokens or the filesystem.
type fakeResolvedClient struct{}

func (fakeResolvedClient) Name() string { return "fake" }
func (fakeResolvedClient) ListRepos(context.Context, string) ([]forge.RepoRef, error) {
	return nil, nil
}
func (fakeResolvedClient) ListOpenIssues(context.Context, string, string) ([]forge.Issue, error) {
	return nil, nil
}
func (fakeResolvedClient) GetFile(context.Context, string, string, string) ([]byte, string, bool, error) {
	return nil, "", false, nil
}
func (fakeResolvedClient) CreateIssue(context.Context, string, string, string, string) (forge.Issue, error) {
	return forge.Issue{}, nil
}

func TestNewCachingClientResolver_ResolvesOncePerKey(t *testing.T) {
	calls := 0
	resolve := func(forgeName, owner string) forge.Client {
		calls++
		if forgeName == "github" && owner == "acme" {
			return fakeResolvedClient{}
		}
		return nil
	}
	cached := newCachingClientResolver(resolve)

	if c := cached("github", "acme"); c == nil {
		t.Fatal("want non-nil client for configured target")
	}
	if c := cached("github", "acme"); c == nil {
		t.Fatal("want non-nil client on second call")
	}
	if calls != 1 {
		t.Fatalf("want resolve called once for a repeated key, got %d", calls)
	}

	if c := cached("forgejo", "freax"); c != nil {
		t.Fatalf("want nil client for unconfigured target, got %v", c)
	}
	if calls != 2 {
		t.Fatalf("want resolve called for a new key, got %d", calls)
	}

	if c := cached("forgejo", "freax"); c != nil {
		t.Fatalf("want nil client on repeated call, got %v", c)
	}
	if calls != 2 {
		t.Fatalf("want a nil result cached too (no repeat resolve), got %d calls", calls)
	}
}

// readerOnlyClient satisfies forge.Client — and therefore imcp.ForgeReader —
// while implementing none of the capability interfaces. This is the GitLab/ADO
// shape.
type readerOnlyClient struct{}

func (readerOnlyClient) Name() string { return "readeronly" }
func (readerOnlyClient) ListRepos(context.Context, string) ([]forge.RepoRef, error) {
	return nil, nil
}
func (readerOnlyClient) ListOpenIssues(context.Context, string, string) ([]forge.Issue, error) {
	return nil, nil
}

func TestNewCachingClientResolver_ReaderOnlyClientResolvesNonNil(t *testing.T) {
	cached := newCachingClientResolver(func(string, string) forge.Client { return readerOnlyClient{} })

	if c := cached("gitlab", "acme"); c == nil {
		t.Fatal("a client with only tier-1 capabilities must resolve non-nil; " +
			"the old type assertion dropped it to nil, which callers misreported as unconfigured")
	}
}

func TestParseOwners(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []imcp.Target
	}{
		{"empty", "", nil},
		{"single", "github:freaxnx01", []imcp.Target{{Forge: "github", Owner: "freaxnx01"}}},
		{"comma-and-space", "github:freaxnx01, forgejo:freax", []imcp.Target{
			{Forge: "github", Owner: "freaxnx01"}, {Forge: "forgejo", Owner: "freax"},
		}},
		{"skips-malformed", "github:freaxnx01 bogus forgejo:freax", []imcp.Target{
			{Forge: "github", Owner: "freaxnx01"}, {Forge: "forgejo", Owner: "freax"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOwners(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseOwners(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseOwners(%q) = %+v, want %+v", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestValidateNoAuthHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		noAuth  bool
		wantErr bool
	}{
		{"auth required, non-loopback host ok", "0.0.0.0", false, false},
		{"no-auth loopback ipv4", "127.0.0.1", true, false},
		{"no-auth localhost", "localhost", true, false},
		{"no-auth loopback ipv6", "::1", true, false},
		{"no-auth non-loopback ip", "0.0.0.0", true, true},
		{"no-auth hostname", "example.com", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNoAuthHost(tt.host, tt.noAuth)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateNoAuthHost(%q, %v) error = %v, wantErr %v", tt.host, tt.noAuth, err, tt.wantErr)
			}
		})
	}
}

func TestBuildMCPHandler_FailFastWithoutToken(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	_, err := buildMCPHandler(srv, "", false)
	if err == nil {
		t.Fatal("want error when token empty and auth required, got nil")
	}
}

func TestBuildMCPHandler_NoAuthSkipsToken(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	h, err := buildMCPHandler(srv, "", true)
	if err != nil || h == nil {
		t.Fatalf("no-auth must not require a token: h=%v err=%v", h, err)
	}
}

func TestBuildMCPHandler_RejectsMissingBearer(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	h, err := buildMCPHandler(srv, "s3cret", false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing bearer: want 401, got %d", resp.StatusCode)
	}
}

// bearerRoundTripper injects a static Authorization header on every request.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func TestBuildMCPHandler_ValidBearerListsTools(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	h, err := buildMCPHandler(srv, "s3cret", false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx := context.Background()
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: "s3cret", base: http.DefaultTransport}},
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect with bearer: %v", err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != 6 {
		t.Fatalf("read-only server: want 6 tools over HTTP, got %d", len(res.Tools))
	}
}

// fakeDiscovery serves a minimal OIDC discovery document over httptest so
// buildOAuthHandler's startup discovery never touches the real network or
// resolves a real DNS name.
func fakeDiscovery(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "https://auth.example.com/authorize",
			"token_endpoint":         "https://auth.example.com/token",
			"userinfo_endpoint":      "https://auth.example.com/userinfo",
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestBuildOAuthHandler_RoutesAndMiddlewarePlacement(t *testing.T) {
	discovery := fakeDiscovery(t)

	dir := t.TempDir()
	cfg := imcpoauth.Config{
		Issuer:              "https://bridge-mcp.example.com",
		AuthentikIssuer:     discovery.URL,
		ClientID:            "cid",
		ClientSecret:        "secret",
		AllowedSubject:      "sub-123",
		StateDir:            dir,
		AllowedRedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
	}
	srv := imcp.NewServer(imcp.Deps{})

	handler, closeFn, err := buildOAuthHandler(srv, cfg, discovery.Client())
	if err != nil {
		t.Fatalf("buildOAuthHandler: %v", err)
	}
	defer func() { _ = closeFn() }() // best-effort release; test process is exiting regardless

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"AS metadata is unauthenticated", http.MethodGet, "/.well-known/oauth-authorization-server", http.StatusOK},
		{"resource metadata is unauthenticated", http.MethodGet, "/.well-known/oauth-protected-resource", http.StatusOK},
		{"MCP root requires a token", http.MethodGet, "/", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("%s %s = %d, want %d", tt.method, tt.path, rec.Code, tt.wantStatus)
			}
		})
	}

	// The OAuth flow endpoints must be reachable without a bearer token: a
	// client cannot present a token it has not yet obtained. These requests
	// carry no Authorization header; a 401 here would mean the route got
	// mounted behind the bearer-token guard instead of beside it.
	unguarded := []struct {
		name   string
		method string
		path   string
	}{
		{"register is reachable without a bearer token", http.MethodPost, "/oauth/register"},
		{"authorize is reachable without a bearer token", http.MethodGet, "/oauth/authorize"},
	}
	for _, tt := range unguarded {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s %s = 401: route is mounted behind the bearer-token guard; OAuth flow endpoints must stay outside it since a client cannot present a token it has not yet obtained", tt.method, tt.path)
			}
		})
	}
}

func TestBuildMCPHandler_StaticModeUnchanged(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{})

	if _, err := buildMCPHandler(srv, "", false); err == nil {
		t.Error("want an error when a token is required but empty")
	}
	if _, err := buildMCPHandler(srv, "tok", false); err != nil {
		t.Errorf("static mode with a token: %v", err)
	}
	if _, err := buildMCPHandler(srv, "", true); err != nil {
		t.Errorf("--no-auth mode: %v", err)
	}
}
