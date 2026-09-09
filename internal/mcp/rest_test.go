package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

// restRequest posts body to /api/tools/<tool> and returns the recorder.
func restRequest(t *testing.T, deps Deps, tool, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/tools/"+tool, strings.NewReader(body))
	w := httptest.NewRecorder()
	RESTHandler(deps).ServeHTTP(w, r)
	return w
}

func TestRESTHandler_UnknownTool(t *testing.T) {
	w := restRequest(t, Deps{}, "nope", `{}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestRESTHandler_WrongMethod(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/tools/read_file", nil)
	w := httptest.NewRecorder()
	RESTHandler(Deps{}).ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", w.Code)
	}
}

func TestRESTHandler_MalformedBody(t *testing.T) {
	w := restRequest(t, Deps{}, "read_file", `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestRESTHandler_ReadFile_ReturnsHandlerOutput(t *testing.T) {
	gh := newFakeFull("github")
	gh.fakeFiles.file = []byte("hello")
	gh.fakeFiles.sha = "abc123"
	gh.fakeFiles.found = true
	deps := depsWith(map[string]*fakeFull{"github": gh}, nil)

	w := restRequest(t, deps, "read_file",
		`{"forge":"github","owner":"o","repo":"r","path":"README.md"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var out readFileOutput
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Found || out.Content != "hello" || out.SHA != "abc123" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestRESTHandler_ListTree_ReturnsHandlerOutput(t *testing.T) {
	gh := newFakeFull("github")
	gh.fakeTree.entries = []forge.TreeEntry{{Path: "a.md", Type: "file"}}
	deps := depsWith(map[string]*fakeFull{"github": gh}, nil)

	w := restRequest(t, deps, "list_tree",
		`{"forge":"github","owner":"o","repo":"r","path":""}`)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var out listTreeOutput
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Path != "a.md" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestRESTHandler_SearchCode_ForgejoTargetSurfacesWarning(t *testing.T) {
	// A Forgejo target has no code-search API — the MCP handler puts that in
	// warnings rather than returning a silent empty result. That behaviour
	// must survive the REST hop, otherwise a REST caller cannot tell "no
	// matches" from "we did not actually look."
	gh := newFakeFull("github")
	deps := depsWith(map[string]*fakeFull{"github": gh},
		[]Target{{Forge: "forgejo", Owner: "freax"}})

	w := restRequest(t, deps, "search_code",
		`{"query":"needle"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var out searchCodeOutput
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Warnings) == 0 {
		t.Fatalf("want warnings surfaced, got %+v", out)
	}
}

func TestRESTHandler_ReadFile_HandlerErrorIs500(t *testing.T) {
	deps := depsWith(map[string]*fakeFull{}, nil) // no github client → handler errors

	w := restRequest(t, deps, "read_file",
		`{"forge":"github","owner":"o","repo":"r","path":"README.md"}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d (%s)", w.Code, w.Body.String())
	}
}
