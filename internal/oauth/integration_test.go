package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// TestEndToEnd_FullConnectorFlow walks the path a Claude connector takes:
// discovery, dynamic registration, authorization, the authentik login, the
// token exchange, and finally an authenticated call.
func TestEndToEnd_FullConnectorFlow(t *testing.T) {
	store, accessToken := obtainAccessTokenViaFlow(t)

	// The access token authenticates at the resource server.
	info, err := store.Verifier()(context.Background(), accessToken, nil)
	if err != nil {
		t.Fatalf("issued access token rejected: %v", err)
	}
	if info.UserID != "sub-123" {
		t.Errorf("UserID = %q, want sub-123", info.UserID)
	}
}

// TestEndToEnd_BearerMiddlewareGuardsProtectedRoute drives a token minted by
// the real flow through the same auth.RequireBearerToken middleware
// production wires in front of the MCP transport (cmd/bridge/mcp.go's
// buildOAuthHandler). It exists because every other test either checks the
// unauthenticated 401 path or calls store.Verifier() directly, so nothing
// asserted that a real, valid token actually reaches the protected handler
// through the middleware.
func TestEndToEnd_BearerMiddlewareGuardsProtectedRoute(t *testing.T) {
	store, accessToken := obtainAccessTokenViaFlow(t)

	protected := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	guarded := sdkauth.RequireBearerToken(store.Verifier(), nil)(protected)
	front := httptest.NewServer(guarded)
	defer front.Close()

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"valid token reaches the handler", "Bearer " + accessToken, http.StatusOK},
		{"no header is rejected", "", http.StatusUnauthorized},
		{"garbage token is rejected", "Bearer not-a-real-token", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, front.URL, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := front.Client().Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// obtainAccessTokenViaFlow runs the full connector flow (discovery, dynamic
// registration, authorization, the authentik login, and the token exchange)
// and returns the resulting store and the minted access token, so tests that
// need a real token don't hand-mint one.
func obtainAccessTokenViaFlow(t *testing.T) (*Store, string) {
	t.Helper()
	fake := newFakeAuthentik(t, "sub-123")

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	cfg := validConfig()
	cfg.AllowedSubject = "sub-123"
	authServer := NewServer(cfg, store)
	authServer.httpClient = fake.Client()

	eps, err := DiscoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	authServer.authentik = eps

	front := httptest.NewServer(authServer.Handler())
	defer front.Close()
	cfg.Issuer = front.URL
	authServer.cfg = cfg

	// 1. AS metadata advertises DCR.
	metaResp, err := front.Client().Get(front.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	defer metaResp.Body.Close()
	var meta struct {
		Registration string `json:"registration_endpoint"`
	}
	if err := json.NewDecoder(metaResp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if meta.Registration == "" {
		t.Fatal("no registration_endpoint advertised")
	}

	// 2. Dynamic client registration.
	regResp, err := front.Client().Post(front.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["`+registeredRedirect+`"],"client_name":"Claude"}`))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer regResp.Body.Close()
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(regResp.Body).Decode(&reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}

	// 3. Authorize — do not follow the redirect to authentik.
	noRedirect := front.Client()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	const verifier = "the-verifier-value-must-be-long-enough"
	authURL := front.URL + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {reg.ClientID},
		"redirect_uri":          {registeredRedirect},
		"state":                 {"client-state"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
	}.Encode()

	authResp, err := noRedirect.Get(authURL)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", authResp.StatusCode)
	}
	upstream, err := url.Parse(authResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse upstream redirect: %v", err)
	}
	sessionID := upstream.Query().Get("state")

	// 4. authentik returns the user to the callback.
	cbResp, err := noRedirect.Get(front.URL + "/oauth/callback?" + url.Values{
		"state": {sessionID}, "code": {"good-code"},
	}.Encode())
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", cbResp.StatusCode)
	}
	back, err := url.Parse(cbResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	code := back.Query().Get("code")
	if code == "" {
		t.Fatal("no authorization code returned")
	}
	if got := back.Query().Get("state"); got != "client-state" {
		t.Errorf("state = %q, want client-state", got)
	}

	// 5. Token exchange.
	tokResp, err := front.Client().PostForm(front.URL+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {reg.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {registeredRedirect},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", tokResp.StatusCode)
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}

	return store, tokens.AccessToken
}
