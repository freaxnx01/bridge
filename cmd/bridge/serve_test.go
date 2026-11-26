package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
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

func sameOriginReq(host, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/pipeline/enrich", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func testAllowlist() originAllowlist {
	return newOriginAllowlist(7777, "bridge.home.example, https://other.example:8443/")
}

func TestRequireSameOrigin_MissingOrigin_403(t *testing.T) {
	w := httptest.NewRecorder()
	requireSameOrigin(testAllowlist(), okHandler()).ServeHTTP(w, sameOriginReq("localhost:7777", ""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRequireSameOrigin_ForeignOrigin_403(t *testing.T) {
	for _, origin := range []string{"http://evil.example", "http://localhost:9999", "null", "::bad"} {
		w := httptest.NewRecorder()
		requireSameOrigin(testAllowlist(), okHandler()).ServeHTTP(w, sameOriginReq("localhost:7777", origin))
		if w.Code != http.StatusForbidden {
			t.Errorf("origin %q: status = %d, want 403", origin, w.Code)
		}
	}
}

func TestRequireSameOrigin_DNSRebinding_OriginEqualsHost_403(t *testing.T) {
	w := httptest.NewRecorder()
	requireSameOrigin(testAllowlist(), okHandler()).ServeHTTP(w, sameOriginReq("evil.example:7777", "http://evil.example:7777"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (Origin == Host must not suffice)", w.Code)
	}
}

func TestRequireSameOrigin_Allowed_PassesThrough(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:7777", "http://127.0.0.1:7777",
		"https://bridge.home.example", "https://other.example:8443",
	} {
		w := httptest.NewRecorder()
		requireSameOrigin(testAllowlist(), okHandler()).ServeHTTP(w, sameOriginReq("whatever:1", origin))
		if w.Code != http.StatusOK {
			t.Errorf("origin %q: status = %d, want 200", origin, w.Code)
		}
	}
}

func TestRequireTokenConfigured_Empty_503AndNoCall(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	w := httptest.NewRecorder()
	requireTokenConfigured("", next).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/pipeline/enrich", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "BRIDGE_API_TOKEN not set") || called {
		t.Fatalf("got %d %s called=%v", w.Code, w.Body, called)
	}
}

func TestRequireTokenConfigured_Set_PassesThrough(t *testing.T) {
	w := httptest.NewRecorder()
	requireTokenConfigured("x", okHandler()).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRequireSameOrigin_CaseInsensitive_Allowed(t *testing.T) {
	al := newOriginAllowlist(7777, "Bridge.Home.Example")
	for _, origin := range []string{"http://BRIDGE.home.example", "http://LocalHost:7777"} {
		w := httptest.NewRecorder()
		requireSameOrigin(al, okHandler()).ServeHTTP(w, sameOriginReq("localhost:7777", origin))
		if w.Code != http.StatusOK {
			t.Errorf("origin %q: status = %d, want 200", origin, w.Code)
		}
	}
}

// clearRoutes are the lock-clearing endpoints; they must be guarded exactly
// like /api/pipeline/enrich, which starts the same paid session.
var clearRoutes = []string{"/api/pipeline/clear-lock", "/api/pipeline/clear-reenrich"}

func guardedChain(token string, next http.Handler) http.Handler {
	return requireTokenConfigured(token, requireBearer(token, requireSameOrigin(testAllowlist(), next)))
}

func clearGuardReq(path, origin, auth string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:7777"+path, strings.NewReader(`{"repo":"o/r","number":612}`))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return req
}

func TestClearRoutes_GuardedLikeEnrich(t *testing.T) {
	cases := []struct {
		name, token, origin, auth string
		want                      int
	}{
		{"no token configured", "", "http://localhost:7777", "Bearer secret", http.StatusServiceUnavailable},
		{"no bearer", "secret", "http://localhost:7777", "", http.StatusUnauthorized},
		{"wrong bearer", "secret", "http://localhost:7777", "Bearer nope", http.StatusUnauthorized},
		{"foreign origin", "secret", "http://evil.example", "Bearer secret", http.StatusForbidden},
		{"allowed", "secret", "http://localhost:7777", "Bearer secret", http.StatusOK},
	}
	for _, path := range clearRoutes {
		for _, tc := range cases {
			t.Run(path+" "+tc.name, func(t *testing.T) {
				reached := false
				next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					reached = true
					w.WriteHeader(http.StatusOK)
				})
				w := httptest.NewRecorder()
				guardedChain(tc.token, next).ServeHTTP(w, clearGuardReq(path, tc.origin, tc.auth))
				if w.Code != tc.want {
					t.Fatalf("status = %d, want %d (%s)", w.Code, tc.want, w.Body)
				}
				if reached != (tc.want == http.StatusOK) {
					t.Fatalf("handler reached = %v for status %d", reached, w.Code)
				}
			})
		}
	}
}

// The inbox routes as runServe registers them: the merge write is the only
// guarded route under the open /api/inbox/ prefix, so this drives the real
// mux, not a hand-built chain — it proves both that the guard wraps the merge
// handler and that the exact /api/inbox/merge pattern wins over the subtree.
func TestInboxRoutes_MergeGuarded_ReadsOpen(t *testing.T) {
	merged := false
	h := &api.InboxHandler{
		Inbox: func() cockpit.Inbox { return cockpit.Inbox{} },
		Merge: func(context.Context, api.MergeRequest) (api.MergeResult, error) {
			merged = true
			return api.MergeResult{Merged: true}, nil
		},
	}
	body := `{"repo":"o/a","number":5,"pr":9,"headSha":"96fae5fd5d9aa4022a3b5a57a69b50510a563891"}`
	cases := []struct {
		name, token, origin, auth string
		want                      int
	}{
		{"no token configured", "", "http://localhost:7777", "Bearer secret", http.StatusServiceUnavailable},
		{"no bearer", "secret", "http://localhost:7777", "", http.StatusUnauthorized},
		{"wrong bearer", "secret", "http://localhost:7777", "Bearer nope", http.StatusUnauthorized},
		{"foreign origin", "secret", "http://evil.example", "Bearer secret", http.StatusForbidden},
		{"allowed", "secret", "http://localhost:7777", "Bearer secret", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run("merge "+tc.name, func(t *testing.T) {
			merged = false
			mux := http.NewServeMux()
			registerInboxRoutes(mux, h, diffNoop(), tc.token, testAllowlist())
			req := httptest.NewRequest(http.MethodPost, "http://localhost:7777/api/inbox/merge", strings.NewReader(body))
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.want, w.Body)
			}
			if merged != (tc.want == http.StatusOK) {
				t.Fatalf("merge reached = %v for status %d", merged, w.Code)
			}
		})
	}
	t.Run("inbox read stays open", func(t *testing.T) {
		mux := http.NewServeMux()
		registerInboxRoutes(mux, h, diffNoop(), "secret", testAllowlist())
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:7777/api/inbox", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/inbox = %d, want 200 without a token", w.Code)
		}
	})
}

// diffNoop is a DiffHandler whose loader answers an empty diff.
func diffNoop() *api.DiffHandler {
	return &api.DiffHandler{
		Diff: func(context.Context, string, string, int) (cockpit.Diff, error) { return cockpit.Diff{}, nil },
	}
}

// The diff is the one read that serves source code, so it needs the bearer
// token — but no Origin: a browser sends none on a same-origin GET, and a
// bearer header is never attached cross-site.
func TestInboxRoutes_DiffNeedsTheTokenButNoOrigin(t *testing.T) {
	h := &api.InboxHandler{Inbox: func() cockpit.Inbox { return cockpit.Inbox{} }}
	loaded := false
	diffH := &api.DiffHandler{
		Diff: func(context.Context, string, string, int) (cockpit.Diff, error) {
			loaded = true
			return cockpit.Diff{PR: 9}, nil
		},
	}
	cases := []struct {
		name, token, origin, auth string
		want                      int
	}{
		{"no token configured", "", "http://localhost:7777", "Bearer secret", http.StatusServiceUnavailable},
		{"no bearer", "secret", "http://localhost:7777", "", http.StatusUnauthorized},
		{"wrong bearer", "secret", "http://localhost:7777", "Bearer nope", http.StatusUnauthorized},
		{"no origin at all", "secret", "", "Bearer secret", http.StatusOK},
		{"foreign origin", "secret", "http://evil.example", "Bearer secret", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loaded = false
			mux := http.NewServeMux()
			registerInboxRoutes(mux, h, diffH, tc.token, testAllowlist())
			req := httptest.NewRequest(http.MethodGet,
				"http://localhost:7777/api/inbox/diff?repo=o/a&number=5", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.want, w.Body)
			}
			if loaded != (tc.want == http.StatusOK) {
				t.Fatalf("diff loaded = %v for status %d", loaded, w.Code)
			}
		})
	}
}
