package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"time"
)

// verifyPKCE reports whether verifier hashes to challenge under S256.
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// handleToken implements the token endpoint. It is unauthenticated by spec
// (public clients have no client secret) and therefore internet-reachable, so
// the request body is bounded like the other unauthenticated endpoints.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.handleAuthorizationCodeGrant(w, r)
	case "refresh_token":
		s.handleRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (s *Server) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request) {
	var (
		clientID    = r.PostForm.Get("client_id")
		rawCode     = r.PostForm.Get("code")
		verifier    = r.PostForm.Get("code_verifier")
		redirectURI = r.PostForm.Get("redirect_uri")
		now         = time.Now()
	)

	codeHash := hashSecret(rawCode)

	s.store.mu.Lock()
	rec, ok := s.store.st.Codes[codeHash]
	var saveErr error
	if ok {
		// Single-use: consume it regardless of whether validation passes, so a
		// failed attempt cannot be retried against the same code.
		delete(s.store.st.Codes, codeHash)
		s.store.prune(now)
		saveErr = s.store.save()
	}
	s.store.mu.Unlock()

	if saveErr != nil {
		slog.Error("persist consumed authorization code failed", "err", saveErr)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist code consumption")
		return
	}

	if !ok || !rec.ExpiresAt.After(now) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is unknown or expired")
		return
	}
	if rec.ClientID != clientID || rec.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code was not issued for this client and redirect_uri")
		return
	}
	if !verifyPKCE(verifier, rec.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	chainID, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	access, refresh, err := s.store.IssueTokenPair(rec.ClientID, rec.Subject, chainID, now)
	if err != nil {
		slog.Error("issue token pair failed", "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
	})
}

// handleRefreshGrant rotates a refresh token. Presenting one that has already
// been consumed means the token leaked, so the entire chain is revoked rather
// than just refusing the request.
func (s *Server) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	var (
		clientID = r.PostForm.Get("client_id")
		raw      = r.PostForm.Get("refresh_token")
		now      = time.Now()
	)

	s.store.mu.Lock()
	rec, ok := s.store.st.Tokens[hashSecret(raw)]
	switch {
	case !ok || rec.Kind != KindRefresh:
		s.store.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
		return
	case rec.Consumed:
		chain := rec.ChainID
		s.store.revokeChain(chain)
		saveErr := s.store.save()
		s.store.mu.Unlock()
		slog.Warn("refresh token reuse detected; chain revoked", "client_id", rec.ClientID, "sub", rec.Subject)
		if saveErr != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not revoke chain")
			return
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected; re-authorization required")
		return
	case !rec.ExpiresAt.After(now):
		s.store.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	case rec.ClientID != clientID:
		s.store.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token was not issued to this client")
		return
	}

	rec.Consumed = true
	subject, chainID := rec.Subject, rec.ChainID
	saveErr := s.store.save()
	s.store.mu.Unlock()

	if saveErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not rotate token")
		return
	}

	access, refresh, err := s.store.IssueTokenPair(clientID, subject, chainID, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
	})
}
