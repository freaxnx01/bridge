package oauth

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const authCodeTTL = 60 * time.Second

// handleCallback completes the authentik leg: it consumes the login session,
// resolves the authenticated subject, enforces the single-user rule, and hands
// the client its own authorization code.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("state")

	// Single-use: take the session out of the map immediately.
	s.sessMu.Lock()
	sess, ok := s.sessions[sessionID]
	delete(s.sessions, sessionID)
	s.sessMu.Unlock()

	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown or already-used login session")
		return
	}
	if time.Since(sess.CreatedAt) > loginSessionTTL {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "login session expired; start again")
		return
	}
	if upstreamErr := r.URL.Query().Get("error"); upstreamErr != "" {
		slog.Warn("authentik returned an error", "client_id", sess.ClientID, "error", upstreamErr)
		writeOAuthError(w, http.StatusBadRequest, "access_denied", "identity provider returned an error")
		return
	}

	subject, err := s.exchangeAndIdentify(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		slog.Warn("authentik exchange failed", "client_id", sess.ClientID, "err", err)
		writeOAuthError(w, http.StatusBadGateway, "server_error", "identity provider exchange failed")
		return
	}
	if subject != s.cfg.AllowedSubject {
		slog.Warn("login rejected: subject not permitted", "client_id", sess.ClientID, "outcome", "rejected")
		writeOAuthError(w, http.StatusForbidden, "access_denied", "this account is not permitted to use bridge")
		return
	}

	code, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue code")
		return
	}

	now := time.Now()
	s.store.mu.Lock()
	s.store.st.Codes[hashSecret(code)] = &Code{
		ClientID:      sess.ClientID,
		RedirectURI:   sess.RedirectURI,
		CodeChallenge: sess.CodeChallenge,
		Subject:       subject,
		ExpiresAt:     now.Add(authCodeTTL),
	}
	if c, ok := s.store.st.Clients[sess.ClientID]; ok {
		c.LastUsedAt = now
	}
	s.store.prune(now)
	saveErr := s.store.save()
	s.store.mu.Unlock()

	if saveErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist code")
		return
	}

	slog.Info("login accepted", "client_id", sess.ClientID, "sub", subject, "outcome", "accepted")

	dest, err := url.Parse(sess.RedirectURI)
	if err != nil { // already validated at /authorize; unreachable in practice
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "invalid stored redirect_uri")
		return
	}
	q := dest.Query()
	q.Set("code", code)
	if sess.ClientState != "" {
		q.Set("state", sess.ClientState)
	}
	dest.RawQuery = q.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}
