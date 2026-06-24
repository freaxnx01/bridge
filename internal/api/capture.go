package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/freaxnx01/bridge/internal/forge"
)

// CaptureHandler handles POST /api/capture/idea and POST /api/capture/issue.
type CaptureHandler struct {
	Idea   func(ctx context.Context, target, text string) (string, error)
	Issue  func(ctx context.Context, owner, repo, title string) (forge.Issue, error)
	Notify func(eventType string, data any)
}

func (h *CaptureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kind := strings.TrimPrefix(r.URL.Path, "/api/capture/")
	switch kind {
	case "idea":
		h.captureIdea(w, r)
	case "issue":
		h.captureIssue(w, r)
	default:
		writeError(w, http.StatusNotFound, "unknown capture kind")
	}
}

type ideaRequest struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

func (h *CaptureHandler) captureIdea(w http.ResponseWriter, r *http.Request) {
	var req ideaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Target == "" || req.Text == "" {
		writeError(w, http.StatusBadRequest, "target and text are required")
		return
	}
	url, err := h.Idea(r.Context(), req.Target, req.Text)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	writeJSON(w, map[string]string{"url": url})
}

type issueRequest struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Title string `json:"title"`
}

func (h *CaptureHandler) captureIssue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Owner == "" || req.Repo == "" || req.Title == "" {
		writeError(w, http.StatusBadRequest, "owner, repo, and title are required")
		return
	}
	issue, err := h.Issue(r.Context(), req.Owner, req.Repo, req.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	writeJSON(w, issue)
}
