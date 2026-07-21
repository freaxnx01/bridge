package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// handleRegister implements RFC 7591 dynamic client registration. Claude assumes
// it is not pre-registered, so this endpoint is unauthenticated by spec. It is
// therefore internet-reachable: every registration is bounded by
// enforceClientCap to keep the state file from growing without limit.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body is not valid JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect_uri is required")
		return
	}
	for _, raw := range req.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri must be an absolute URL")
			return
		}
		if !slices.Contains(s.cfg.AllowedRedirectURIs, raw) {
			// Registration is unauthenticated by spec (RFC 7591), so an
			// attacker could otherwise register a redirect_uri they control
			// and steal an authorization code by borrowing the authorized
			// user's login. Only pre-configured destinations are accepted.
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri is not in the configured allowlist")
			return
		}
	}

	clientID, err := newSecret()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate client id")
		return
	}

	now := time.Now()
	s.store.mu.Lock()
	s.store.st.Clients[clientID] = &Client{
		RedirectURIs: req.RedirectURIs,
		Name:         req.ClientName,
		CreatedAt:    now,
	}
	s.store.enforceClientCap(now)
	saveErr := s.store.save()
	s.store.mu.Unlock()

	if saveErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist registration")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              req.RedirectURIs,
		"client_name":                req.ClientName,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}
