package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

// newSecret returns a 256-bit random secret, base64url encoded without padding.
// Used for access tokens, refresh tokens, authorization codes, and client IDs.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSecret returns the hex SHA-256 of s. Only hashes are persisted, so a
// leaked state file yields no usable credentials.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// IssueTokenPair mints an access/refresh pair belonging to one rotation chain.
func (s *Store) IssueTokenPair(clientID, subject, chainID string, now time.Time) (string, string, error) {
	access, err := newSecret()
	if err != nil {
		return "", "", err
	}
	refresh, err := newSecret()
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.st.Tokens[hashSecret(access)] = &Token{
		Kind: KindAccess, ClientID: clientID, Subject: subject,
		ChainID: chainID, ExpiresAt: now.Add(accessTokenTTL),
	}
	s.st.Tokens[hashSecret(refresh)] = &Token{
		Kind: KindRefresh, ClientID: clientID, Subject: subject,
		ChainID: chainID, ExpiresAt: now.Add(refreshTokenTTL),
	}
	s.prune(now)
	if err := s.save(); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Verifier returns the auth.TokenVerifier guarding the MCP transport. It
// mirrors StaticBearerVerifier's shape so the transport wiring is unchanged.
// Only access tokens authenticate MCP calls; a refresh token presented here is
// rejected.
func (s *Store) Verifier() auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if token == "" {
			return nil, auth.ErrInvalidToken
		}
		s.mu.Lock()
		defer s.mu.Unlock()

		rec, ok := s.st.Tokens[hashSecret(token)]
		if !ok || rec.Kind != KindAccess || !rec.ExpiresAt.After(time.Now()) {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			UserID:     rec.Subject,
			Expiration: rec.ExpiresAt,
		}, nil
	}
}
