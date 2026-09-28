package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestParsePathAllowlist(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want imcp.PathAllowlist
	}{
		{"empty-falls-back-to-default", "", imcp.DefaultPathAllowlist},
		{"single", "docs/**", imcp.PathAllowlist{"docs/**"}},
		{"comma-and-space", "docs/**, *.md", imcp.PathAllowlist{"docs/**", "*.md"}},
		{"skips-empty-entries", "docs/**,,*.md", imcp.PathAllowlist{"docs/**", "*.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePathAllowlist(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parsePathAllowlist(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parsePathAllowlist(%q) = %+v, want %+v", tt.in, got, tt.want)
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
	if len(res.Tools) != 8 {
		t.Fatalf("read-only server: want 8 tools over HTTP, got %d", len(res.Tools))
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

// listGitForgesCall is a raw tools/call for list_git_forges, which makes no
// network requests, so it exercises the transport without any forge fakes.
const listGitForgesCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_git_forges","arguments":{}}}`

// listGitForgesResult is a fragment only list_git_forges' own output carries.
// Asserting on it rather than on "result" is what makes the transport tests
// prove the tool ran: a result envelope wrapping isError content would contain
// "result" too.
const listGitForgesResult = `"read_only":true`

// rawMCPRequest sends one raw HTTP request to the MCP endpoint with the
// headers a Streamable HTTP client sends, overridden by hdr (an empty value
// deletes the header). Returns status, headers and body.
func rawMCPRequest(t *testing.T, url, method, body string, hdr map[string]string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }() // read fully below; close error is not actionable in a test
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

func TestBuildMCPHandler_StatelessTransport_ServesWithoutSession(t *testing.T) {
	modes := []struct {
		name   string
		token  string
		noAuth bool
		auth   map[string]string
	}{
		{"no-auth", "", true, nil},
		{"bearer", "s3cret", false, map[string]string{"Authorization": "Bearer s3cret"}},
	}
	cases := []struct {
		name            string
		method          string
		body            string
		hdr             map[string]string
		wantStatus      int
		wantContentType string // "" = don't check
		wantBody        string // substring; "" = don't check
		wantAllow       string // "" = don't check
	}{
		{"tools/call without initialize or session id", http.MethodPost, listGitForgesCall, nil,
			http.StatusOK, "application/json", listGitForgesResult, ""},
		{"made-up session id is not rejected", http.MethodPost, listGitForgesCall,
			map[string]string{"Mcp-Session-Id": "never-issued"},
			http.StatusOK, "application/json", listGitForgesResult, ""},
		{"initialize answers with json", http.MethodPost,
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"c","version":"v0"},"protocolVersion":"2025-06-18","capabilities":{}}}`,
			map[string]string{"MCP-Protocol-Version": "2025-06-18"},
			http.StatusOK, "application/json", `"protocolVersion"`, ""},
		{"initialized notification is accepted", http.MethodPost,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil,
			http.StatusAccepted, "", "", ""},
		{"GET offers no stream", http.MethodGet, "",
			map[string]string{"Accept": "text/event-stream", "Content-Type": ""},
			http.StatusMethodNotAllowed, "", "", "POST"},
		{"DELETE on shutdown is a no-op", http.MethodDelete, "",
			map[string]string{"Mcp-Session-Id": "never-issued"},
			http.StatusNoContent, "", "", ""},
	}
	for _, m := range modes {
		h, err := buildMCPHandler(imcp.NewServer(imcp.Deps{ReadOnly: true}), m.token, m.noAuth)
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(h)
		t.Cleanup(ts.Close)
		for _, tc := range cases {
			t.Run(m.name+"/"+tc.name, func(t *testing.T) {
				hdr := map[string]string{}
				for k, v := range m.auth {
					hdr[k] = v
				}
				for k, v := range tc.hdr {
					hdr[k] = v
				}
				status, header, body := rawMCPRequest(t, ts.URL, tc.method, tc.body, hdr)
				if status != tc.wantStatus {
					t.Fatalf("status = %d, want %d (body %q)", status, tc.wantStatus, body)
				}
				if tc.wantContentType != "" && header.Get("Content-Type") != tc.wantContentType {
					t.Errorf("Content-Type = %q, want %q", header.Get("Content-Type"), tc.wantContentType)
				}
				if tc.wantBody != "" && !strings.Contains(body, tc.wantBody) {
					t.Errorf("body %q does not contain %q", body, tc.wantBody)
				}
				if tc.wantAllow != "" && header.Get("Allow") != tc.wantAllow {
					t.Errorf("Allow = %q, want %q", header.Get("Allow"), tc.wantAllow)
				}
			})
		}
	}
}

// TestNewStreamableHandler_StatelessContract pins the stateless transport at
// the shared constructor rather than only through buildMCPHandler.
// buildOAuthHandler mounts the same handler behind a bearer guard no test
// outside internal/oauth can pass, so without an assertion here the OAuth call
// site could drift back to a stateful NewStreamableHTTPHandler with the whole
// suite staying green.
func TestNewStreamableHandler_StatelessContract(t *testing.T) {
	ts := httptest.NewServer(newStreamableHandler(imcp.NewServer(imcp.Deps{ReadOnly: true})))
	defer ts.Close()

	t.Run("stale session id still serves a tools/call", func(t *testing.T) {
		status, header, body := rawMCPRequest(t, ts.URL, http.MethodPost, listGitForgesCall,
			map[string]string{"Mcp-Session-Id": "never-issued"})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", status, body)
		}
		if ct := header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}
		if !strings.Contains(body, listGitForgesResult) {
			t.Errorf("body %q does not contain %q", body, listGitForgesResult)
		}
	})

	t.Run("GET offers no stream", func(t *testing.T) {
		status, header, body := rawMCPRequest(t, ts.URL, http.MethodGet, "",
			map[string]string{"Accept": "text/event-stream", "Content-Type": ""})
		if status != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405 (body %q)", status, body)
		}
		if allow := header.Get("Allow"); allow != "POST" {
			t.Errorf("Allow = %q, want %q", allow, "POST")
		}
	})
}

// TestBuildMCPHandler_NoAuthGuardsAgainstCrossOriginPost pins the CSRF barrier
// the stateless transport removed: with no bearer token to guess, a page in the
// user's browser would otherwise be able to POST a mutating tools/call at
// 127.0.0.1 and have it execute on the first request, even though it cannot
// read the reply.
func TestBuildMCPHandler_NoAuthGuardsAgainstCrossOriginPost(t *testing.T) {
	h, err := buildMCPHandler(imcp.NewServer(imcp.Deps{ReadOnly: true}), "", true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	tests := []struct {
		name       string
		hdr        map[string]string
		wantStatus int
	}{
		{"cross-origin POST is refused", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site fetch metadata is refused", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		// A CORS-safelisted Content-Type is what lets a browser POST without a
		// preflight the server would never answer; the go-sdk handler rejects it,
		// and this case fails if that ever becomes permissive.
		{"non-JSON content type is refused", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"same-origin POST is served", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"non-browser POST without an Origin is served", nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _, body := rawMCPRequest(t, ts.URL, http.MethodPost, listGitForgesCall, tt.hdr)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", status, tt.wantStatus, body)
			}
		})
	}
}

func TestBuildMCPHandler_StatelessStillRequiresBearer(t *testing.T) {
	h, err := buildMCPHandler(imcp.NewServer(imcp.Deps{ReadOnly: true}), "s3cret", false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	status, _, body := rawMCPRequest(t, ts.URL, http.MethodPost, listGitForgesCall,
		map[string]string{"Mcp-Session-Id": "never-issued"})
	if status != http.StatusUnauthorized {
		t.Fatalf("session id without bearer: status = %d, want 401 (body %q)", status, body)
	}
}
