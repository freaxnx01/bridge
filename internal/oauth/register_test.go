package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(validConfig(), store)
}

func TestHandleRegister_IssuesPublicClient(t *testing.T) {
	srv := newTestServer(t)
	body := `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude"}`
	rec := httptest.NewRecorder()

	srv.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body)
	}
	var resp struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ClientID == "" {
		t.Error("client_id empty")
	}
	if resp.ClientSecret != "" {
		t.Error("client_secret issued; Claude registers as a public client")
	}

	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	if _, ok := srv.store.st.Clients[resp.ClientID]; !ok {
		t.Error("registration not persisted")
	}
}

func TestHandleRegister_RejectsBadRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no redirect_uris", `{"client_name":"Claude"}`},
		{"empty redirect_uris", `{"redirect_uris":[]}`},
		{"malformed json", `{`},
		{"non-absolute redirect", `{"redirect_uris":["/relative"]}`},
		{"redirect outside allowlist", `{"redirect_uris":["https://evil.example/cb"]}`},
		{"second redirect outside allowlist", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback","https://evil.example/cb"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			rec := httptest.NewRecorder()
			srv.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestHandleRegister_EnforcesClientCap(t *testing.T) {
	srv := newTestServer(t)
	// Pre-fill at the cap with used registrations so eviction is observable.
	srv.store.mu.Lock()
	for i := 0; i < maxClients; i++ {
		srv.store.st.Clients[fmt.Sprintf("pre%03d", i)] = &Client{
			CreatedAt:  time.Now().Add(-time.Duration(maxClients-i) * time.Minute),
			LastUsedAt: time.Now().Add(-time.Duration(maxClients-i) * time.Minute),
		}
	}
	srv.store.mu.Unlock()

	rec := httptest.NewRecorder()
	srv.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	srv.store.mu.Lock()
	defer srv.store.mu.Unlock()
	if n := len(srv.store.st.Clients); n > maxClients {
		t.Errorf("client count = %d, exceeds cap %d (unbounded state growth is a DoS vector)", n, maxClients)
	}
}
