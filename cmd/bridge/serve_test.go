package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	imcp "github.com/freaxnx01/bridge/internal/mcp"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestRequireBearer_NoTokenConfigured_PassesThrough(t *testing.T) {
	h := requireBearer("", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (auth disabled)", w.Code)
	}
}

func TestRequireBearer_MissingHeader_401(t *testing.T) {
	h := requireBearer("secret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRequireBearer_WrongToken_401(t *testing.T) {
	h := requireBearer("secret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	req.Header.Set("Authorization", "Bearer nope")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRequireBearer_CorrectToken_PassesThrough(t *testing.T) {
	h := requireBearer("secret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// toolsMux mirrors the /api/tools/ wiring in runServe so the bearer guard
// is exercised by tests without spinning up the whole HTTP server.
func toolsMux(token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/tools/", requireBearer(token, imcp.RESTHandler(imcp.Deps{})))
	return mux
}

func TestAPIMux_ToolsRequiresBearer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/tools/read_file", nil)
	w := httptest.NewRecorder()
	toolsMux("secret").ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAPIMux_ToolsAcceptsValidBearer(t *testing.T) {
	// Only assert not-401 — the empty Deps will error inside the handler,
	// but that means we got past the bearer wrapper, which is the point.
	req := httptest.NewRequest(http.MethodPost, "/api/tools/read_file", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	toolsMux("secret").ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = 401, want the bearer to have passed through")
	}
}

func TestRestDeps_WritesDisabledByDefault(t *testing.T) {
	// `bridge serve` previously had no write surface at all. Adding /api/tools/
	// must not quietly turn the WebUI port into a file-writing endpoint —
	// especially since requireBearer disables auth entirely when
	// BRIDGE_API_TOKEN is unset.
	got := restDeps(imcp.Deps{ReadOnly: false}, false)
	if !got.ReadOnly {
		t.Fatal("bridge serve must not expose put_file over REST by default")
	}
}

func TestRestDeps_AllowWritesEnablesTheWritePath(t *testing.T) {
	got := restDeps(imcp.Deps{ReadOnly: false}, true)
	if got.ReadOnly {
		t.Fatal("--allow-writes should enable the REST write path")
	}
}

func TestRestDeps_ExistingReadOnlySettingWins(t *testing.T) {
	// BRIDGE_MCP_READONLY=1 must not be overridable by --allow-writes.
	got := restDeps(imcp.Deps{ReadOnly: true}, true)
	if !got.ReadOnly {
		t.Fatal("an explicit read-only setting must win over --allow-writes")
	}
}
