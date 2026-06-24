package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestCaptureHandler_Idea_Returns200(t *testing.T) {
	var notified string
	h := &CaptureHandler{
		Idea: func(_ context.Context, target, text string) (string, error) {
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
		Issue: func(_ context.Context, owner, repo, title string) (forge.Issue, error) {
			return forge.Issue{Title: title, URL: "https://github.com/alice/myrepo/issues/1"}, nil
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
		Idea: func(_ context.Context, _, _ string) (string, error) { return "", nil },
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
