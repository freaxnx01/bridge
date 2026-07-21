package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// AuthentikEndpoints are the OIDC endpoints bridge uses as a confidential
// client of authentik.
type AuthentikEndpoints struct {
	Authorization string
	Token         string
	UserInfo      string
}

// DiscoverAuthentik reads the provider's OIDC discovery document.
func DiscoverAuthentik(ctx context.Context, issuer string, c *http.Client) (*AuthentikEndpoints, error) {
	metaURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", metaURL, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only response body; close error carries nothing actionable
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery %s: %s", metaURL, resp.Status)
	}

	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		UserInfoEndpoint      string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse discovery %s: %w", metaURL, err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.UserInfoEndpoint == "" {
		return nil, fmt.Errorf("discovery %s: missing required endpoints", metaURL)
	}
	return &AuthentikEndpoints{
		Authorization: doc.AuthorizationEndpoint,
		Token:         doc.TokenEndpoint,
		UserInfo:      doc.UserInfoEndpoint,
	}, nil
}

// exchangeAndIdentify swaps authentik's authorization code for an access token
// and resolves the authenticated subject via userinfo.
//
// The token is obtained directly from the token endpoint over TLS, so OIDC Core
// §3.1.3.7 does not require validating an ID token signature; using userinfo
// keeps this package free of any JWT dependency.
func (s *Server) exchangeAndIdentify(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.cfg.endpoint("/oauth/callback"))
	form.Set("client_id", s.cfg.ClientID)
	form.Set("client_secret", s.cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authentik.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only response body; close error carries nothing actionable
	if resp.StatusCode != http.StatusOK {
		// Deliberately does not include the body: it can echo the code.
		return "", fmt.Errorf("token exchange rejected: %s", resp.Status)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token response contained no access_token")
	}

	uiReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.authentik.UserInfo, nil)
	if err != nil {
		return "", fmt.Errorf("build userinfo request: %w", err)
	}
	uiReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	uiResp, err := s.httpClient.Do(uiReq)
	if err != nil {
		return "", fmt.Errorf("userinfo: %w", err)
	}
	defer func() { _ = uiResp.Body.Close() }() // read-only response body; close error carries nothing actionable
	if uiResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo rejected: %s", uiResp.Status)
	}
	var ui struct {
		Sub string `json:"sub"`
	}
	if err := json.NewDecoder(uiResp.Body).Decode(&ui); err != nil {
		return "", fmt.Errorf("parse userinfo: %w", err)
	}
	if ui.Sub == "" {
		return "", fmt.Errorf("userinfo contained no sub")
	}
	return ui.Sub, nil
}
