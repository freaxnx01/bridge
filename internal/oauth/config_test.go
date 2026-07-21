package oauth

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Issuer:              "https://bridge-mcp.example.com",
		AuthentikIssuer:     "https://auth.example.com/application/o/bridge/",
		ClientID:            "cid",
		ClientSecret:        "secret",
		AllowedSubject:      "sub-123",
		StateDir:            "/tmp/state",
		AllowedRedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
	}
}

func TestConfigValidate_ReportsEveryMissingValueAtOnce(t *testing.T) {
	err := Config{StateDir: "/tmp/state"}.Validate()
	if err == nil {
		t.Fatal("want error for empty config")
	}
	for _, want := range []string{
		"BRIDGE_MCP_ISSUER", "BRIDGE_OIDC_ISSUER",
		"BRIDGE_OIDC_CLIENT_ID", "BRIDGE_OIDC_CLIENT_SECRET", "BRIDGE_OIDC_ALLOWED_SUB",
		"BRIDGE_MCP_ALLOWED_REDIRECT_URIS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestConfigValidate_AllowedRedirectURIs(t *testing.T) {
	tests := []struct {
		name    string
		uris    []string
		wantErr bool
	}{
		{"absolute https url", []string{"https://claude.ai/api/mcp/auth_callback"}, false},
		{"multiple absolute urls", []string{"https://claude.ai/api/mcp/auth_callback", "https://example.com/cb"}, false},
		{"relative path rejected", []string{"/relative"}, true},
		{"empty entry rejected", []string{""}, true},
		{"malformed url rejected", []string{"://nope"}, true},
		{"one bad entry among good ones rejected", []string{"https://claude.ai/api/mcp/auth_callback", "/relative"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			c.AllowedRedirectURIs = tt.uris
			err := c.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("uris %v: want error, got nil", tt.uris)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("uris %v: unexpected error %v", tt.uris, err)
			}
		})
	}
}

func TestConfigValidate_IssuerScheme(t *testing.T) {
	tests := []struct {
		name    string
		issuer  string
		wantErr bool
	}{
		{"https", "https://bridge-mcp.example.com", false},
		{"loopback http allowed for dev", "http://127.0.0.1:7788", false},
		{"localhost http allowed for dev", "http://localhost:7788", false},
		{"plain http rejected", "http://bridge-mcp.example.com", true},
		{"not a url", "://nope", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			c.Issuer = tt.issuer
			err := c.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("issuer %q: want error, got nil", tt.issuer)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("issuer %q: unexpected error %v", tt.issuer, err)
			}
		})
	}
}
