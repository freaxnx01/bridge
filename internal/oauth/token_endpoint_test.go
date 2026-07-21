package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// seedCode inserts an authorization code directly.
func seedCode(t *testing.T, srv *Server, clientID, redirect, challenge string, expiry time.Time) string {
	t.Helper()
	code, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	srv.store.st.Codes[hashSecret(code)] = &Code{
		ClientID: clientID, RedirectURI: redirect,
		CodeChallenge: challenge, Subject: validConfig().AllowedSubject,
		ExpiresAt: expiry,
	}
	return code
}

func tokenRequest(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func codeGrantForm(clientID, code, verifier, redirect string) url.Values {
	f := url.Values{}
	f.Set("grant_type", "authorization_code")
	f.Set("client_id", clientID)
	f.Set("code", code)
	f.Set("code_verifier", verifier)
	f.Set("redirect_uri", redirect)
	return f
}

func TestHandleToken_CodeGrantIssuesTokens(t *testing.T) {
	srv := newTestServer(t)
	cid := registerClient(t, srv, registeredRedirect)
	const verifier = "the-verifier-value-must-be-long-enough"
	code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))

	rec := httptest.NewRecorder()
	srv.handleToken(rec, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", resp)
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", resp.TokenType)
	}
	if resp.ExpiresIn != int(accessTokenTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", resp.ExpiresIn, int(accessTokenTTL.Seconds()))
	}
}

func TestHandleToken_CodeGrantRejections(t *testing.T) {
	const verifier = "the-verifier-value-must-be-long-enough"

	tests := []struct {
		name    string
		mutate  func(t *testing.T, srv *Server, cid string, form url.Values)
		wantErr bool
	}{
		{
			name:   "happy path",
			mutate: func(*testing.T, *Server, string, url.Values) {},
		},
		{
			name:    "wrong verifier",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("code_verifier", "wrong-verifier") },
			wantErr: true,
		},
		{
			name:    "missing verifier",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Del("code_verifier") },
			wantErr: true,
		},
		{
			name:    "redirect_uri mismatch",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("redirect_uri", "https://evil.com/cb") },
			wantErr: true,
		},
		{
			name:    "client_id mismatch",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("client_id", "another-client") },
			wantErr: true,
		},
		{
			name:    "unknown code",
			mutate:  func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("code", "no-such-code") },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			cid := registerClient(t, srv, registeredRedirect)
			code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))
			form := codeGrantForm(cid, code, verifier, registeredRedirect)
			tt.mutate(t, srv, cid, form)

			rec := httptest.NewRecorder()
			srv.handleToken(rec, tokenRequest(form))

			if tt.wantErr && rec.Code == http.StatusOK {
				t.Errorf("request accepted; want rejection (body %s)", rec.Body)
			}
			if !tt.wantErr && rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
			}
		})
	}
}

func TestHandleToken_CodeIsSingleUseAndExpires(t *testing.T) {
	const verifier = "the-verifier-value-must-be-long-enough"

	t.Run("second use fails", func(t *testing.T) {
		srv := newTestServer(t)
		cid := registerClient(t, srv, registeredRedirect)
		code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))

		first := httptest.NewRecorder()
		srv.handleToken(first, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
		if first.Code != http.StatusOK {
			t.Fatalf("first exchange failed: %d %s", first.Code, first.Body)
		}

		second := httptest.NewRecorder()
		srv.handleToken(second, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
		if second.Code == http.StatusOK {
			t.Error("authorization code replay succeeded")
		}
	})

	t.Run("expired code fails", func(t *testing.T) {
		srv := newTestServer(t)
		cid := registerClient(t, srv, registeredRedirect)
		code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(-time.Second))

		rec := httptest.NewRecorder()
		srv.handleToken(rec, tokenRequest(codeGrantForm(cid, code, verifier, registeredRedirect)))
		if rec.Code == http.StatusOK {
			t.Error("expired code accepted")
		}
	})
}
