package oauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestVerifier_AcceptsIssuedAccessTokenOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	now := time.Now()
	access, refresh, err := s.IssueTokenPair("cid", "sub-123", "chain-1", now)
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	verify := s.Verifier()

	info, err := verify(context.Background(), access, nil)
	if err != nil {
		t.Fatalf("valid access token rejected: %v", err)
	}
	if info.UserID != "sub-123" {
		t.Errorf("UserID = %q, want sub-123 (needed for session binding)", info.UserID)
	}
	if !info.Expiration.After(now) {
		t.Errorf("Expiration %v not in the future", info.Expiration)
	}

	// A refresh token must not authenticate MCP calls.
	if _, err := verify(context.Background(), refresh, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("refresh token accepted at the resource server: %v", err)
	}
}

func TestVerifier_RejectsUnknownExpiredAndRevoked(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	valid, _, err := s.IssueTokenPair("cid", "sub-123", "chain-1", time.Now())
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	expired, _, err := s.IssueTokenPair("cid", "sub-123", "chain-2", time.Now().Add(-2*accessTokenTTL))
	if err != nil {
		t.Fatalf("IssueTokenPair: %v", err)
	}

	revoked := valid
	s.mu.Lock()
	delete(s.st.Tokens, hashSecret(revoked))
	s.mu.Unlock()

	verify := s.Verifier()
	tests := []struct {
		name  string
		token string
	}{
		{"unknown", "not-a-real-token"},
		{"empty", ""},
		{"expired", expired},
		{"revoked", revoked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := verify(context.Background(), tt.token, nil); !errors.Is(err, auth.ErrInvalidToken) {
				t.Errorf("token %q: want ErrInvalidToken, got %v", tt.name, err)
			}
		})
	}
}
