package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/freaxnx01/bridge/internal/cockpit"
)

// ErrAmbiguousRepo is returned by a launch func when more than one repo shares
// the requested basename (bridge launch resolves by basename).
var ErrAmbiguousRepo = errors.New("ambiguous repo name")

// maxLaunchBody bounds the request body of the action endpoints.
const maxLaunchBody = 4 << 10

// LaunchRequest names the issue an action applies to.
type LaunchRequest struct {
	Repo   string `json:"repo"` // "owner/name"
	Number int    `json:"number"`
}

// LaunchResult is what a started (or already running) session reports.
type LaunchResult struct {
	Slot           string `json:"slot"`
	AlreadyRunning bool   `json:"alreadyRunning"`
	RCURL          string `json:"rcUrl,omitempty"`
}

// PipelineHandler serves the Pipeline board and its actions:
// GET /api/pipeline, POST /api/pipeline/{refresh,enrich,dispatch}.
type PipelineHandler struct {
	Board    func() cockpit.Board
	Refresh  func(ctx context.Context)
	Enrich   func(ctx context.Context, req LaunchRequest) (LaunchResult, error)
	Dispatch func(ctx context.Context, req LaunchRequest) (LaunchResult, error)
}

func (h *PipelineHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := http.MethodPost
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/api/pipeline":
		method = http.MethodGet
	case "/api/pipeline/refresh", "/api/pipeline/enrich", "/api/pipeline/dispatch":
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/api/pipeline":
		writeJSON(w, h.Board())
	case "/api/pipeline/refresh":
		h.Refresh(r.Context())
		w.WriteHeader(http.StatusAccepted)
	case "/api/pipeline/enrich":
		h.enrich(w, r)
	case "/api/pipeline/dispatch":
		h.dispatch(w, r)
	}
}

func (h *PipelineHandler) enrich(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLaunchRequest(w, r)
	if !ok {
		return
	}
	card, found := findCard(h.Board().NeedsEnrichment, req)
	if !found {
		writeError(w, http.StatusConflict, "not on the board")
		return
	}
	if card.Forge != "github" {
		writeError(w, http.StatusUnprocessableEntity, "headless enrich is GitHub-only")
		return
	}
	h.respond(w, r, h.Enrich, req)
}

func (h *PipelineHandler) dispatch(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLaunchRequest(w, r)
	if !ok {
		return
	}
	if _, found := findCard(h.Board().Ready, req); !found {
		writeError(w, http.StatusConflict, "not on the board")
		return
	}
	h.respond(w, r, h.Dispatch, req)
}

func (h *PipelineHandler) respond(w http.ResponseWriter, r *http.Request,
	launch func(context.Context, LaunchRequest) (LaunchResult, error), req LaunchRequest) {
	res, err := launch(r.Context(), req)
	if errors.Is(err, ErrAmbiguousRepo) {
		writeError(w, http.StatusConflict, ErrAmbiguousRepo.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, res)
}

func decodeLaunchRequest(w http.ResponseWriter, r *http.Request) (LaunchRequest, bool) {
	var req LaunchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLaunchBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return req, false
	}
	if req.Number <= 0 {
		writeError(w, http.StatusBadRequest, "number must be positive")
		return req, false
	}
	if strings.Count(req.Repo, "/") != 1 || strings.HasPrefix(req.Repo, "/") || strings.HasSuffix(req.Repo, "/") {
		writeError(w, http.StatusBadRequest, "repo must be owner/name")
		return req, false
	}
	return req, true
}

func findCard(cards []cockpit.IssueCard, req LaunchRequest) (cockpit.IssueCard, bool) {
	for _, c := range cards {
		if c.Repo == req.Repo && c.Number == req.Number {
			return c, true
		}
	}
	return cockpit.IssueCard{}, false
}
