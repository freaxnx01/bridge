package oauth

import (
	"net/http"
	"strings"
)

func (c Config) endpoint(path string) string {
	return strings.TrimRight(c.Issuer, "/") + path
}

// authServerMetadata is the RFC 8414 document. It advertises exactly the three
// capabilities authentik lacks and Claude requires: dynamic client
// registration, S256 PKCE, and public clients.
func (c Config) authServerMetadata() map[string]any {
	return map[string]any{
		"issuer":                                c.Issuer,
		"authorization_endpoint":                c.endpoint("/oauth/authorize"),
		"token_endpoint":                        c.endpoint("/oauth/token"),
		"registration_endpoint":                 c.endpoint("/oauth/register"),
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
}

func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.authServerMetadata())
}
