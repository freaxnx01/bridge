package oauth

import "testing"

func TestRedirectURIAllowed(t *testing.T) {
	remote := "https://claude.ai/api/mcp/auth_callback"
	loopbackAnchor := "http://localhost/callback"

	tests := []struct {
		name      string
		allowed   []string
		candidate string
		want      bool
	}{
		// Remote redirects: byte-exact only, unchanged behaviour.
		{"remote exact match", []string{remote}, remote, true},
		{"remote mismatch", []string{remote}, "https://evil.example/cb", false},
		{"remote path prefix is not a match", []string{remote}, remote + "/../evil", false},
		{"remote host must be exact even with loopback anchor", []string{loopbackAnchor}, "http://evil.example/callback", false},

		// Loopback redirects: port-agnostic against a loopback anchor.
		{"loopback different port matches anchor", []string{loopbackAnchor}, "http://localhost:58883/callback", true},
		{"loopback exact anchor (no port) matches", []string{loopbackAnchor}, "http://localhost/callback", true},
		{"loopback 127.0.0.1 matches localhost anchor", []string{loopbackAnchor}, "http://127.0.0.1:49152/callback", true},
		{"loopback ::1 matches localhost anchor", []string{loopbackAnchor}, "http://[::1]:49152/callback", true},
		{"loopback candidate, anchor is 127.0.0.1", []string{"http://127.0.0.1/callback"}, "http://localhost:7000/callback", true},

		// Loopback relaxation is anchored: without a matching loopback entry it is refused.
		{"loopback but no loopback anchor", []string{remote}, "http://localhost:58883/callback", false},
		{"loopback path must match anchor path", []string{loopbackAnchor}, "http://localhost:58883/evil", false},
		{"loopback scheme must match anchor scheme", []string{"https://localhost/callback"}, "http://localhost:58883/callback", false},
		{"loopback query must match anchor query", []string{loopbackAnchor}, "http://localhost:58883/callback?x=1", false},

		// Empty allowlist never matches.
		{"empty allowlist", nil, "http://localhost:58883/callback", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redirectURIAllowed(tt.allowed, tt.candidate); got != tt.want {
				t.Errorf("redirectURIAllowed(%q, %q) = %v, want %v", tt.allowed, tt.candidate, got, tt.want)
			}
		})
	}
}
