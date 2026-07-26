package oauth

import (
	"net/url"
	"slices"
)

// redirectURIAllowed reports whether candidate is an acceptable redirect_uri
// for the configured allowlist. It is the single gate used at both
// /oauth/register and /oauth/authorize.
//
// A remote (non-loopback) candidate must match an allowlist entry
// byte-for-byte. No normalisation, prefix, or wildcard matching — those are the
// classic OAuth redirect bypasses, and a remote redirect_uri is the actual
// code-theft vector, since /oauth/register is unauthenticated (RFC 7591).
//
// Loopback candidates are the one, narrow exception. RFC 8252 §7.3 has native
// apps receive the authorization response on http://127.0.0.1 (or ::1, or
// localhost) at a port the OS assigns at runtime, so the port cannot be known
// ahead of time and pinned in the allowlist — which is exactly why a CLI like
// Claude Code, whose callback port changes every launch, can never match a
// fixed entry. For a loopback candidate we therefore ignore the port and treat
// the three loopback hosts as equivalent, but STILL require an allowlist entry
// that is itself loopback and matches on scheme and path (and query). The admin
// thus opts in per callback path (e.g. add "http://localhost/callback"), and
// the relaxation never touches a non-loopback host: a remote attacker's
// redirect_uri still has to be byte-exact.
func redirectURIAllowed(allowed []string, candidate string) bool {
	if slices.Contains(allowed, candidate) {
		return true
	}
	cu, err := url.Parse(candidate)
	if err != nil || !isLoopback(cu.Hostname()) {
		return false
	}
	for _, entry := range allowed {
		au, err := url.Parse(entry)
		if err != nil {
			continue
		}
		if isLoopback(au.Hostname()) &&
			au.Scheme == cu.Scheme &&
			au.EscapedPath() == cu.EscapedPath() &&
			au.RawQuery == cu.RawQuery {
			return true
		}
	}
	return false
}
