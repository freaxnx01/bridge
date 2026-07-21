package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

const registeredRedirect = "https://claude.ai/api/mcp/auth_callback"

// registerClient inserts a client directly, bypassing the HTTP layer.
func registerClient(t *testing.T, srv *Server, redirects ...string) string {
	t.Helper()
	id, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	srv.store.st.Clients[id] = &Client{RedirectURIs: redirects, CreatedAt: time.Now()}
	return id
}

func authorizeRequest(clientID, redirect, challenge, method string) *http.Request {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", "client-state")
	if challenge != "" {
		q.Set("code_challenge", challenge)
	}
	if method != "" {
		q.Set("code_challenge_method", method)
	}
	return httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
}

func TestHandleAuthorize_RedirectURIMustMatchExactly(t *testing.T) {
	tests := []struct {
		name     string
		redirect string
	}{
		{"prefix extension", registeredRedirect + ".evil.com"},
		{"extra path segment", registeredRedirect + "/extra"},
		{"trailing slash", registeredRedirect + "/"},
		{"scheme downgrade", "http://claude.ai/api/mcp/auth_callback"},
		{"different port", "https://claude.ai:8443/api/mcp/auth_callback"},
		{"added query", registeredRedirect + "?next=https://evil.com"},
		{"embedded userinfo", "https://claude.ai@evil.com/api/mcp/auth_callback"},
		{"host case variation", "https://CLAUDE.AI/api/mcp/auth_callback"},
		{"unrelated host", "https://evil.com/cb"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			cid := registerClient(t, srv, registeredRedirect)
			rec := httptest.NewRecorder()

			srv.handleAuthorize(rec, authorizeRequest(cid, tt.redirect, "challenge-value", "S256"))

			// A bad redirect_uri must never be redirected to — that is the
			// open-redirect / code-theft path. Report it locally instead.
			if rec.Code == http.StatusFound || rec.Code == http.StatusSeeOther {
				t.Fatalf("redirected to unvalidated redirect_uri %q (Location %q)", tt.redirect, rec.Header().Get("Location"))
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for redirect_uri %q", rec.Code, tt.redirect)
			}
		})
	}
}

func TestHandleAuthorize_AcceptsExactMatchAndRedirectsToAuthentik(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	srv.authentik = &authentikEndpoints{Authorization: "https://auth.example.com/authorize"}
	rec := httptest.NewRecorder()

	srv.handleAuthorize(rec, authorizeRequest(cid, registeredRedirect, "challenge-value", "S256"))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Host != "auth.example.com" {
		t.Errorf("redirected to %q, want authentik", loc.Host)
	}
	if loc.Query().Get("state") == "" {
		t.Error("no state on the authentik leg; the callback could not be correlated")
	}
}

func TestHandleAuthorize_PKCERules(t *testing.T) {
	tests := []struct {
		name      string
		challenge string
		method    string
		wantErr   bool
	}{
		{"S256 accepted", "challenge-value", "S256", false},
		{"plain rejected", "challenge-value", "plain", true},
		{"missing method rejected", "challenge-value", "", true},
		{"missing challenge rejected", "", "S256", true},
		{"unknown method rejected", "challenge-value", "S512", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			cid := registerClient(t, srv, registeredRedirect)
			srv.authentik = &authentikEndpoints{Authorization: "https://auth.example.com/authorize"}
			rec := httptest.NewRecorder()

			srv.handleAuthorize(rec, authorizeRequest(cid, registeredRedirect, tt.challenge, tt.method))

			if tt.wantErr && rec.Code == http.StatusFound {
				t.Errorf("PKCE %q/%q accepted; want rejection", tt.challenge, tt.method)
			}
			if !tt.wantErr && rec.Code != http.StatusFound {
				t.Errorf("status = %d, want 302", rec.Code)
			}
		})
	}
}

func TestHandleAuthorize_UnknownClientRejected(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleAuthorize(rec, authorizeRequest("no-such-client", registeredRedirect, "challenge", "S256"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
