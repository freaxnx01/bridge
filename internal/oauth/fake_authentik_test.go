package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newFakeAuthentik stands in for the identity provider: discovery, token
// exchange, and userinfo. Hand-rolled so the whole flow runs in-process.
func newFakeAuthentik(t *testing.T, subject string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"userinfo_endpoint":      srv.URL + "/userinfo",
			"jwks_uri":               srv.URL + "/jwks",
		}); err != nil {
			t.Fatalf("encode discovery document: %v", err)
		}
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("code") == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") == "wrong-code" {
			http.Error(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token": "upstream-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}); err != nil {
			t.Fatalf("encode token response: %v", err)
		}
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"sub": subject}); err != nil {
			t.Fatalf("encode userinfo response: %v", err)
		}
	})
	return srv
}
