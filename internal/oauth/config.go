package oauth

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Config is the OAuth-mode configuration, resolved from the environment by
// cmd/bridge. Every field is required.
type Config struct {
	Issuer          string // BRIDGE_MCP_ISSUER — bridge's public base URL
	AuthentikIssuer string // BRIDGE_OIDC_ISSUER
	ClientID        string // BRIDGE_OIDC_CLIENT_ID
	ClientSecret    string // BRIDGE_OIDC_CLIENT_SECRET
	AllowedSubject  string // BRIDGE_OIDC_ALLOWED_SUB — the only permitted sub
	StateDir        string // BRIDGE_MCP_STATE_DIR
}

// Validate reports every problem at once, so a misconfigured deployment is
// fixed in one pass rather than one restart per missing variable.
func (c Config) Validate() error {
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"BRIDGE_MCP_ISSUER", c.Issuer},
		{"BRIDGE_OIDC_ISSUER", c.AuthentikIssuer},
		{"BRIDGE_OIDC_CLIENT_ID", c.ClientID},
		{"BRIDGE_OIDC_CLIENT_SECRET", c.ClientSecret},
		{"BRIDGE_OIDC_ALLOWED_SUB", c.AllowedSubject},
		{"BRIDGE_MCP_STATE_DIR", c.StateDir},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("--auth=oauth requires: %s", strings.Join(missing, ", "))
	}

	u, err := url.Parse(c.Issuer)
	if err != nil || u.Host == "" {
		return fmt.Errorf("BRIDGE_MCP_ISSUER %q is not a valid URL", c.Issuer)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopback(u.Hostname()) {
		return nil // local development only
	}
	return fmt.Errorf("BRIDGE_MCP_ISSUER must use https (got %q); http is allowed only on loopback", c.Issuer)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
