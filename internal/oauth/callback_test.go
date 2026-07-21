package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// startedSession wires a server to the fake IdP and seeds one in-flight login.
func startedSession(t *testing.T, subject string) (*Server, string) {
	t.Helper()
	fake := newFakeAuthentik(t, subject)
	srv := newTestServer(t)
	srv.httpClient = fake.Client()
	eps, err := discoverAuthentik(context.Background(), fake.URL, fake.Client())
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
	if loc.Query().Get("code") == "" {
		t.Error("no authorization code returned to the client")
	}
	if loc.Query().Get("state") != "client-state" {
		t.Errorf("state = %q, want the client's original state", loc.Query().Get("state"))
	}
}

func TestHandleCallback_ForeignSubjectRejected(t *testing.T) {
	srv, sessionID := startedSession(t, "somebody-else")
	rec := httptest.NewRecorder()

	srv.handleCallback(rec, callbackRequest(sessionID, "good-code"))

	if rec.Code == http.StatusFound {
		t.Fatal("a subject other than BRIDGE_OIDC_ALLOWED_SUB completed the login")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
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
