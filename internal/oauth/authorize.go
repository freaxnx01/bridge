package oauth

import (
	"net/http"
	"net/url"
	"slices"
	"time"
)

// loginSession is one in-flight authorization: the client's original request,
// held while the user completes the authentik login. Sessions live in memory
// only — an interrupted login is simply restarted.
type loginSession struct {
	ClientID      string
	RedirectURI   string
	ClientState   string
	CodeChallenge string
	CreatedAt     time.Time
}

const loginSessionTTL = 10 * time.Minute

// handleAuthorize validates the client's request and hands the user off to
// authentik. Every failure here is reported locally rather than by redirecting:
// an unvalidated redirect_uri must never be used as a redirect target, since
// that is exactly the open-redirect and code-theft path.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if q.Get("response_type") != "code" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_response_type", "only response_type=code is supported")
		return
	}

	s.store.mu.Lock()
	client, known := s.store.st.Clients[q.Get("client_id")]
	var registered []string
	if known {
		registered = slices.Clone(client.RedirectURIs)
	}
	s.store.mu.Unlock()

	if !known {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}

	redirectURI := q.Get("redirect_uri")
	if !slices.Contains(registered, redirectURI) {
		// Exact string comparison. No normalisation, prefix, or wildcard
		// matching — those are the classic OAuth redirect bypasses.
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri does not exactly match a registered value")
		return
	}

	challenge := q.Get("code_challenge")
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}

	if s.authentik == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "identity provider metadata unavailable")
		return
	}

	sessionID, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not start login")
		return
	}

	now := time.Now()
	s.sessMu.Lock()
	for id, sess := range s.sessions { // opportunistic cleanup
		if now.Sub(sess.CreatedAt) > loginSessionTTL {
			delete(s.sessions, id)
		}
	}
	s.sessions[sessionID] = &loginSession{
		ClientID:      q.Get("client_id"),
		RedirectURI:   redirectURI,
		ClientState:   q.Get("state"),
		CodeChallenge: challenge,
		CreatedAt:     now,
	}
	s.sessMu.Unlock()

	upstream, err := url.Parse(s.authentik.Authorization)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "invalid identity provider metadata")
		return
	}
	uq := upstream.Query()
	uq.Set("response_type", "code")
	uq.Set("client_id", s.cfg.ClientID)
	uq.Set("redirect_uri", s.cfg.endpoint("/oauth/callback"))
	uq.Set("scope", "openid profile")
	uq.Set("state", sessionID)
	upstream.RawQuery = uq.Encode()

	http.Redirect(w, r, upstream.String(), http.StatusFound)
}
