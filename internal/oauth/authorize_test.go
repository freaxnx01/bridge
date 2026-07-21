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
	sessionID := loc.Query().Get("state")
	if sessionID == "" {
		t.Fatal("no state on the authentik leg; the callback could not be correlated")
	}

	srv.sessMu.Lock()
	sess, ok := srv.sessions[sessionID]
	srv.sessMu.Unlock()
	if !ok {
		t.Fatalf("no session stored for id %q", sessionID)
	}
	if sess.RedirectURI != registeredRedirect {
		t.Errorf("session RedirectURI = %q, want the registered value %q", sess.RedirectURI, registeredRedirect)
	}
	if sess.ClientState != "client-state" {
		t.Errorf("session ClientState = %q, want %q", sess.ClientState, "client-state")
	}
	if sess.CodeChallenge != "challenge-value" {
		t.Errorf("session CodeChallenge = %q, want %q", sess.CodeChallenge, "challenge-value")
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

			if tt.wantErr {
				if rec.Code == http.StatusFound {
					t.Fatalf("PKCE %q/%q accepted; want rejection (Location %q)", tt.challenge, tt.method, rec.Header().Get("Location"))
				}
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want 400 for PKCE %q/%q", rec.Code, tt.challenge, tt.method)
				}
			}
			if !tt.wantErr && rec.Code != http.StatusFound {
				t.Errorf("status = %d, want 302", rec.Code)
			}
		})
	}
}

func TestHandleAuthorize_SessionCapRejectsWhenFull(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	srv.authentik = &authentikEndpoints{Authorization: "https://auth.example.com/authorize"}

	srv.sessMu.Lock()
	for i := 0; i < maxLoginSessions; i++ {
		id, err := newSecret()
		if err != nil {
			srv.sessMu.Unlock()
			t.Fatal(err)
		}
		srv.sessions[id] = &loginSession{CreatedAt: time.Now()}
	}
	srv.sessMu.Unlock()

	rec := httptest.NewRecorder()
	srv.handleAuthorize(rec, authorizeRequest(cid, registeredRedirect, "challenge-value", "S256"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the session table is at capacity", rec.Code)
	}

	srv.sessMu.Lock()
	n := len(srv.sessions)
	srv.sessMu.Unlock()
	if n > maxLoginSessions {
		t.Errorf("sessions grew to %d, want capped at %d", n, maxLoginSessions)
	}
}

func TestHandleAuthorize_ExpiredSessionsDoNotConsumeCap(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	srv.authentik = &authentikEndpoints{Authorization: "https://auth.example.com/authorize"}

	// Fill the session table to exactly maxLoginSessions with entries whose
	// CreatedAt is well past the TTL expiry. The sweep should delete all of
	// them before checking the cap, so a new valid request should succeed.
	expiredTime := time.Now().Add(-2 * loginSessionTTL)
	srv.sessMu.Lock()
	for i := 0; i < maxLoginSessions; i++ {
		id, err := newSecret()
		if err != nil {
			srv.sessMu.Unlock()
			t.Fatal(err)
		}
		srv.sessions[id] = &loginSession{CreatedAt: expiredTime}
	}
	lenBefore := len(srv.sessions)
	srv.sessMu.Unlock()

	// Issue a valid authorize request against a table full of expired sessions.
	// The sweep must reclaim the expired entries before checking the cap.
	rec := httptest.NewRecorder()
	srv.handleAuthorize(rec, authorizeRequest(cid, registeredRedirect, "challenge-value", "S256"))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (sweep should have cleared expired entries)", rec.Code)
	}

	srv.sessMu.Lock()
	lenAfter := len(srv.sessions)
	srv.sessMu.Unlock()

	if lenAfter > maxLoginSessions {
		t.Errorf("sessions grew to %d, want at most %d", lenAfter, maxLoginSessions)
	}
	// After the sweep, we should have far fewer entries (just the new one).
	// Before the fix, this would be full of expired corpses.
	if lenAfter >= lenBefore {
		t.Errorf("sweep did not reclaim expired entries: before %d, after %d", lenBefore, lenAfter)
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
