package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestStaticBearerVerifier_ValidInvalidMissing(t *testing.T) {
	verify := StaticBearerVerifier("s3cret")
	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"valid", "s3cret", false},
		{"wrong", "nope", true},
		{"empty", "", true},
		{"prefix-of-valid", "s3cre", true},
		{"superset-of-valid", "s3cretx", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := verify(context.Background(), tt.token, nil)
			if tt.wantErr {
				if !errors.Is(err, auth.ErrInvalidToken) {
					t.Fatalf("token %q: want ErrInvalidToken, got info=%v err=%v", tt.token, info, err)
				}
				return
			}
			if err != nil || info == nil {
				t.Fatalf("token %q: want info, got info=%v err=%v", tt.token, info, err)
			}
			if !info.Expiration.After(time.Now()) {
				t.Errorf("token %q: expiration %v is not in the future", tt.token, info.Expiration)
			}
		})
	}
}
