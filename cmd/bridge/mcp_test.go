package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	imcp "github.com/freaxnx01/bridge/internal/mcp"
)

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
