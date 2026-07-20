package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
	imcp "github.com/freaxnx01/bridge/internal/mcp"
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
	if len(res.Tools) != 3 {
		t.Fatalf("read-only server: want 3 tools over HTTP, got %d", len(res.Tools))
	}
}
