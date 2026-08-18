package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// IssueParams carries a capture-issue request to the injected Issue func. A
// caller supplies either Alias, or Owner+Repo, plus a Title and optional Body.
type IssueParams struct{ Owner, Repo, Alias, Title, Body string }

// IdeaParams carries a capture-idea request to the injected Idea func. A caller
// supplies either Alias or Target, plus the idea Text.
type IdeaParams struct{ Target, Alias, Text string }

// CaptureHandler handles POST /api/capture/idea and POST /api/capture/issue.
type CaptureHandler struct {
	Idea   func(ctx context.Context, p IdeaParams) (string, error)
	Issue  func(ctx context.Context, p IssueParams) (forge.Issue, error)
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
	Alias  string `json:"alias"`
	Text   string `json:"text"`
}

func (h *CaptureHandler) captureIdea(w http.ResponseWriter, r *http.Request) {
	var req ideaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Text == "" || (req.Alias == "" && req.Target == "") {
		writeError(w, http.StatusBadRequest, "text and either alias or target are required")
		return
	}
	url, err := h.Idea(r.Context(), IdeaParams(req))
	if err != nil {
		writeCaptureError(w, err)
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
	Alias string `json:"alias"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (h *CaptureHandler) captureIssue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Title == "" || (req.Alias == "" && (req.Owner == "" || req.Repo == "")) {
		writeError(w, http.StatusBadRequest, "title and either alias or owner+repo are required")
		return
	}
	issue, err := h.Issue(r.Context(), IssueParams(req))
	if err != nil {
		writeCaptureError(w, err)
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	writeJSON(w, issue)
}

// writeCaptureError maps a resolver/creation error to an HTTP status: an
// unknown alias → 404, an ambiguous alias → 409, anything else → 500.
func writeCaptureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrAliasNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, core.ErrAliasAmbiguous):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
