package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func TestCaptureHandler_Idea_Returns200(t *testing.T) {
	var notified string
	h := &CaptureHandler{
		Idea: func(_ context.Context, _ IdeaParams) (string, error) {
			return "https://github.com/alice/ideas/commit/abc", nil
		},
		Notify: func(et string, _ any) { notified = et },
	}
	body := strings.NewReader(`{"target":"ideas-lab","text":"great idea"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/capture/idea", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if notified != "overview-updated" {
		t.Errorf("Notify called with %q, want overview-updated", notified)
	}
}

func TestCaptureHandler_Issue_Returns200(t *testing.T) {
	h := &CaptureHandler{
		Issue: func(_ context.Context, p IssueParams) (forge.Issue, error) {
			return forge.Issue{Title: p.Title, URL: "https://github.com/alice/myrepo/issues/1"}, nil
		},
	}
	body := strings.NewReader(`{"owner":"alice","repo":"myrepo","title":"bug found"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/capture/issue", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestCaptureHandler_MissingFields_Returns400(t *testing.T) {
	h := &CaptureHandler{
		Idea: func(_ context.Context, _ IdeaParams) (string, error) { return "", nil },
	}
	body := strings.NewReader(`{"target":""}`)
	r := httptest.NewRequest(http.MethodPost, "/api/capture/idea", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCaptureHandler_UnknownKind_Returns404(t *testing.T) {
	h := &CaptureHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/capture/roadmap", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestCaptureIssue_AliasAndBody_ForwardedToIssueFunc(t *testing.T) {
	var got IssueParams
	h := &CaptureHandler{
		Issue: func(_ context.Context, p IssueParams) (forge.Issue, error) {
			got = p
			return forge.Issue{Number: 1, URL: "https://forge/issues/1"}, nil
		},
	}
	body := `{"alias":"br","title":"Login 500","body":"the detail"}`
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got.Alias != "br" || got.Title != "Login 500" || got.Body != "the detail" {
		t.Fatalf("params = %+v", got)
	}
}

func TestCaptureIssue_UnknownAlias_Returns404(t *testing.T) {
	h := &CaptureHandler{
		Issue: func(_ context.Context, _ IssueParams) (forge.Issue, error) {
			return forge.Issue{}, core.ErrAliasNotFound
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(`{"alias":"nope","title":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCaptureIssue_AmbiguousAlias_Returns409(t *testing.T) {
	h := &CaptureHandler{
		Issue: func(_ context.Context, _ IssueParams) (forge.Issue, error) {
			return forge.Issue{}, core.ErrAliasAmbiguous
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(`{"alias":"br","title":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCaptureIssue_NoTarget_Returns400(t *testing.T) {
	h := &CaptureHandler{Issue: func(_ context.Context, _ IssueParams) (forge.Issue, error) {
		t.Fatal("Issue should not be called")
		return forge.Issue{}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(`{"title":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCaptureIdea_AliasHappyPath(t *testing.T) {
	var got IdeaParams
	h := &CaptureHandler{
		Idea: func(_ context.Context, p IdeaParams) (string, error) {
			got = p
			return "https://forge/ideas.md#x", nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/idea", strings.NewReader(`{"alias":"agp","text":"what if"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || got.Alias != "agp" || got.Text != "what if" {
		t.Fatalf("code=%d params=%+v", w.Code, got)
	}
}
