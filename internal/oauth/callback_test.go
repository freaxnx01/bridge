package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// startedSession wires a server to the fake IdP and seeds one in-flight login.
func startedSession(t *testing.T, subject string) (*Server, string) {
	t.Helper()
	fake := newFakeAuthentik(t, subject)
	srv := newTestServer(t)
	srv.httpClient = fake.Client()
	eps, err := DiscoverAuthentik(context.Background(), fake.URL, fake.Client())
	if err != nil {
		t.Fatal(err)
	}
	srv.authentik = eps

	cid := registerClient(t, srv, registeredRedirect)
	sessionID, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	srv.sessions[sessionID] = &loginSession{
		ClientID: cid, RedirectURI: registeredRedirect,
		ClientState: "client-state", CodeChallenge: "challenge-value",
		CreatedAt: time.Now(),
	}
	return srv, sessionID
}

func callbackRequest(state, code string) *http.Request {
	q := url.Values{}
	q.Set("state", state)
	q.Set("code", code)
	return httptest.NewRequest(http.MethodGet, "/oauth/callback?"+q.Encode(), nil)
}

func TestHandleCallback_AllowedSubjectGetsRedirectedBackWithCode(t *testing.T) {
	srv, sessionID := startedSession(t, validConfig().AllowedSubject)

	// Snapshot the seeded session's bindings before handleCallback consumes
	// (deletes) it, so we can assert the stored Code record was populated
	// from the session, not from request query parameters.
	srv.sessMu.Lock()
	seeded := *srv.sessions[sessionID]
	srv.sessMu.Unlock()

	rec := httptest.NewRecorder()

	srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != registeredRedirect {
		t.Errorf("redirected to %q, want %q", got, registeredRedirect)
	}
	issued := loc.Query().Get("code")
	if issued == "" {
		t.Fatal("no authorization code returned to the client")
	}
	if loc.Query().Get("state") != "client-state" {
		t.Errorf("state = %q, want the client's original state", loc.Query().Get("state"))
	}

	// Copy state out from under the store's mutex, then assert without it
	// held.
	srv.store.mu.Lock()
	stored, hashedExists := srv.store.st.Codes[hashSecret(issued)]
	_, rawExists := srv.store.st.Codes[issued]
	var record Code
	if stored != nil {
		record = *stored
	}
	srv.store.mu.Unlock()

	if !hashedExists {
		t.Fatal("no Code record found under hashSecret(issued); the store must key codes by their hash so a leaked state file yields no usable credential")
	}
	if rawExists {
		t.Error("a Code record exists under the raw issued code as key; codes must be stored hashed only — storing (or also storing) the raw value defeats the point of hashing")
	}
	wantSubject := validConfig().AllowedSubject
	if record.ClientID != seeded.ClientID {
		t.Errorf("stored ClientID = %q, want %q (the seeded session's, not a request parameter)", record.ClientID, seeded.ClientID)
	}
	if record.RedirectURI != seeded.RedirectURI {
		t.Errorf("stored RedirectURI = %q, want %q (the seeded session's, not a request parameter)", record.RedirectURI, seeded.RedirectURI)
	}
	if record.CodeChallenge != seeded.CodeChallenge {
		t.Errorf("stored CodeChallenge = %q, want %q (the seeded session's, not a request parameter)", record.CodeChallenge, seeded.CodeChallenge)
	}
	if record.Subject != wantSubject {
		t.Errorf("stored Subject = %q, want %q (the identity resolved during exchange)", record.Subject, wantSubject)
	}
	wantExpiry := time.Now().Add(authCodeTTL)
	const tolerance = 5 * time.Second
	if diff := record.ExpiresAt.Sub(wantExpiry); diff < -tolerance || diff > tolerance {
		t.Errorf("stored ExpiresAt = %v, want within %v of %v (authCodeTTL=%v after issuance)", record.ExpiresAt, tolerance, wantExpiry, authCodeTTL)
	}
}

func TestHandleCallback_NearMissSubjectsRejected(t *testing.T) {
	allowed := validConfig().AllowedSubject

	tests := []struct {
		name    string
		subject string
	}{
		{"plainly different", "somebody-else"},
		{"case variant", strings.ToUpper(allowed)},
		{"leading whitespace", " " + allowed},
		{"trailing whitespace", allowed + " "},
		{"allowed subject as a prefix of a longer string", allowed + "-impersonator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, sessionID := startedSession(t, tt.subject)
			rec := httptest.NewRecorder()

			srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))

			if rec.Code == http.StatusFound {
				t.Fatalf("subject %q completed the login; only %q (exact match) is permitted", tt.subject, allowed)
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestHandleCallback_UnknownAndReplayedState(t *testing.T) {
	t.Run("unknown state", func(t *testing.T) {
		srv, _ := startedSession(t, validConfig().AllowedSubject)
		rec := httptest.NewRecorder()
		srv.handleCallback(rec, callbackRequest("no-such-session", "good-code"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("replayed state", func(t *testing.T) {
		srv, sessionID := startedSession(t, validConfig().AllowedSubject)
		srv.handleCallback(httptest.NewRecorder(), callbackRequest(sessionID, "good-code"))

		rec := httptest.NewRecorder()
		srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))
		if rec.Code == http.StatusFound {
			t.Error("session replay succeeded; sessions must be single-use")
		}
	})
}
