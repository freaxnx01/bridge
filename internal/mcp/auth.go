// Package mcp constructs the Bridge cross-forge MCP server: it registers the
// forge tools on an *mcp.Server built from injected dependencies, and provides
// the static-bearer verifier used to guard the HTTP transport. Transport and
// process concerns live in cmd/bridge.
package mcp

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// StaticBearerVerifier returns an auth.TokenVerifier that accepts exactly one
// bearer token (want), compared in constant time. On a match it returns a
// TokenInfo with a far-future expiration (the RequireBearerToken middleware
// rejects expired tokens); otherwise it returns auth.ErrInvalidToken.
func StaticBearerVerifier(want string) auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			Expiration: time.Now().Add(10 * 365 * 24 * time.Hour),
		}, nil
	}
}
