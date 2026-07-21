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

// oauthErrorBody mirrors the shape written by writeOAuthError.
type oauthErrorBody struct {
	Error string `json:"error"`
}

func TestHandleToken_CodeGrantRejections(t *testing.T) {
	const verifier = "the-verifier-value-must-be-long-enough"

	tests := []struct {
		name       string
		mutate     func(t *testing.T, srv *Server, cid string, form url.Values)
		wantStatus int
		wantErr    string
	}{
		{
			name:       "happy path",
			mutate:     func(*testing.T, *Server, string, url.Values) {},
			wantStatus: http.StatusOK,
		},
		{
			name:       "wrong verifier",
			mutate:     func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("code_verifier", "wrong-verifier") },
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_grant",
		},
		{
			name:       "missing verifier",
			mutate:     func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Del("code_verifier") },
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_grant",
		},
		{
			name:       "redirect_uri mismatch",
			mutate:     func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("redirect_uri", "https://evil.com/cb") },
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_grant",
		},
		{
			name:       "client_id mismatch",
			mutate:     func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("client_id", "another-client") },
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_grant",
		},
		{
			name:       "unknown code",
			mutate:     func(_ *testing.T, _ *Server, _ string, f url.Values) { f.Set("code", "no-such-code") },
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_grant",
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

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantErr == "" {
				return
			}
			var body oauthErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal error body: %v", err)
			}
			if body.Error != tt.wantErr {
				t.Errorf("error = %q, want %q", body.Error, tt.wantErr)
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

	// The code must be burned on the very first attempt, even when that
	// attempt fails validation (e.g. a wrong PKCE verifier) — otherwise an
	// attacker holding a stolen code gets a free retry with the correct
	// verifier. This guards the delete-before-validate ordering in
	// handleAuthorizationCodeGrant: moving the delete below the PKCE check
	// would make the second attempt below succeed.
	t.Run("code burned even when first attempt fails PKCE", func(t *testing.T) {
		srv := newTestServer(t)
		cid := registerClient(t, srv, registeredRedirect)
		code := seedCode(t, srv, cid, registeredRedirect, challengeFor(verifier), time.Now().Add(authCodeTTL))

		badForm := codeGrantForm(cid, code, "wrong-verifier", registeredRedirect)
		first := httptest.NewRecorder()
		srv.handleToken(first, tokenRequest(badForm))
		if first.Code != http.StatusBadRequest {
			t.Fatalf("first (bad verifier) status = %d, want %d (body %s)", first.Code, http.StatusBadRequest, first.Body)
		}
		var firstBody oauthErrorBody
		if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
			t.Fatalf("unmarshal first error body: %v", err)
		}
		if firstBody.Error != "invalid_grant" {
			t.Errorf("first error = %q, want invalid_grant", firstBody.Error)
		}

		goodForm := codeGrantForm(cid, code, verifier, registeredRedirect)
		second := httptest.NewRecorder()
		srv.handleToken(second, tokenRequest(goodForm))
		if second.Code != http.StatusBadRequest {
			t.Fatalf("retry with correct verifier status = %d, want %d (body %s) — code was not burned on first failure", second.Code, http.StatusBadRequest, second.Body)
		}
		var secondBody oauthErrorBody
		if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
			t.Fatalf("unmarshal second error body: %v", err)
		}
		if secondBody.Error != "invalid_grant" {
			t.Errorf("retry error = %q, want invalid_grant", secondBody.Error)
		}
	})
}
