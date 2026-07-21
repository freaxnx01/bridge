package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAuthServerMetadata_AdvertisesWhatClaudeNeeds(t *testing.T) {
	srv := &Server{cfg: validConfig()}
	rec := httptest.NewRecorder()

	srv.handleAuthServerMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (responses must not be cached by intermediaries)", cacheControl)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	var meta struct {
		Issuer                 string   `json:"issuer"`
		Authorization          string   `json:"authorization_endpoint"`
		Token                  string   `json:"token_endpoint"`
		Registration           string   `json:"registration_endpoint"`
		CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
		TokenEndpointAuthMeths []string `json:"token_endpoint_auth_methods_supported"`
		GrantTypes             []string `json:"grant_types_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, rec.Body)
	}

	if meta.Issuer != validConfig().Issuer {
		t.Errorf("issuer = %q, want %q", meta.Issuer, validConfig().Issuer)
	}
	if meta.Registration == "" {
		t.Error("registration_endpoint absent — Claude performs DCR and cannot connect without it")
	}
	if !slices.Contains(meta.CodeChallengeMethods, "S256") {
		t.Errorf("code_challenge_methods_supported = %v, want S256", meta.CodeChallengeMethods)
	}
	if slices.Contains(meta.CodeChallengeMethods, "plain") {
		t.Error("plain PKCE advertised; only S256 is accepted")
	}
	if !slices.Contains(meta.TokenEndpointAuthMeths, "none") {
		t.Errorf("token_endpoint_auth_methods_supported = %v, want none (Claude is a public client)", meta.TokenEndpointAuthMeths)
	}
	if !slices.Contains(meta.GrantTypes, "authorization_code") || !slices.Contains(meta.GrantTypes, "refresh_token") {
		t.Errorf("grant_types_supported = %v, want authorization_code and refresh_token", meta.GrantTypes)
	}
}
