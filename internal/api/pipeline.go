package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/freaxnx01/bridge/internal/cockpit"
)

// ErrAmbiguousRepo is returned by a launch func when more than one repo shares
// the requested basename (bridge launch resolves by basename).
var ErrAmbiguousRepo = errors.New("ambiguous repo name")

// ErrUnknownRepo is returned by a launch func when the repo is not a known clone.
var ErrUnknownRepo = errors.New("unknown repo")

// ErrUnsupportedForge is returned by ClearLock when the repo's forge client
// cannot remove a label (everything except GitHub today).
var ErrUnsupportedForge = errors.New("lock clearing is not supported for this forge")

// ErrNoToken is returned by ClearLock when no token resolves for the repo's owner.
var ErrNoToken = errors.New("no token for this owner")

// maxLaunchBody bounds the request body of the action endpoints.
const maxLaunchBody = 4 << 10

// StuckEnrichingAfter is how long an enrichment-ongoing issue may stay
// unchanged before the board calls the lock stuck. Keep equal to
// STUCK_ENRICHING_SECONDS in web/src/lib/running.js.
const StuckEnrichingAfter = 6 * time.Hour

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

// PipelineHandler serves the Pipeline board and its actions: GET /api/pipeline,
// POST /api/pipeline/{refresh,enrich,dispatch,clear-lock,clear-reenrich}.
type PipelineHandler struct {
	Board     func() cockpit.Board
	Refresh   func(ctx context.Context)
	Enrich    func(ctx context.Context, req LaunchRequest) (LaunchResult, error)
	Dispatch  func(ctx context.Context, req LaunchRequest) (LaunchResult, error)
	ClearLock func(ctx context.Context, req LaunchRequest) error // removes enrichment-ongoing
	Now       func() time.Time                                   // nil means time.Now

	inflight sync.Map // LaunchRequest -> struct{}: launches currently starting
}

func (h *PipelineHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, method := r.URL.Path, http.MethodPost
	switch path {
	case "/api/pipeline":
		method = http.MethodGet
	case "/api/pipeline/refresh", "/api/pipeline/enrich", "/api/pipeline/dispatch",
		"/api/pipeline/clear-lock", "/api/pipeline/clear-reenrich":
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch path {
	case "/api/pipeline":
		writeJSON(w, nonNilBoard(h.Board()))
	case "/api/pipeline/refresh":
		h.Refresh(r.Context())
		w.WriteHeader(http.StatusAccepted)
	case "/api/pipeline/enrich":
		h.enrich(w, r)
	case "/api/pipeline/dispatch":
		h.dispatch(w, r)
	case "/api/pipeline/clear-lock":
		h.clearLock(w, r)
	case "/api/pipeline/clear-reenrich":
		h.clearAndReenrich(w, r)
	}
}

// nonNilBoard makes empty sections serialize as [] rather than null.
func nonNilBoard(b cockpit.Board) cockpit.Board {
	if b.Running == nil {
		b.Running = []cockpit.RunningCard{}
	}
	if b.Ready == nil {
		b.Ready = []cockpit.IssueCard{}
	}
	if b.Held == nil {
		b.Held = []cockpit.IssueCard{}
	}
	if b.NeedsEnrichment == nil {
		b.NeedsEnrichment = []cockpit.IssueCard{}
	}
	if b.Errors == nil {
		b.Errors = []cockpit.SourceError{}
	}
	if b.Skipped == nil {
		b.Skipped = []cockpit.SkippedForge{}
	}
	return b
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
	h.exclusive(w, req, func() { h.writeLaunch(w, r, launch, req) })
}

// exclusive runs act unless another action for the same issue is in flight.
func (h *PipelineHandler) exclusive(w http.ResponseWriter, req LaunchRequest, act func()) {
	if _, busy := h.inflight.LoadOrStore(req, struct{}{}); busy {
		writeError(w, http.StatusConflict, "launch already in progress")
		return
	}
	defer h.inflight.Delete(req)
	act()
}

func (h *PipelineHandler) writeLaunch(w http.ResponseWriter, r *http.Request,
	launch func(context.Context, LaunchRequest) (LaunchResult, error), req LaunchRequest) {
	res, err := launch(r.Context(), req)
	switch {
	case errors.Is(err, ErrAmbiguousRepo):
		writeError(w, http.StatusConflict, ErrAmbiguousRepo.Error())
		return
	case errors.Is(err, ErrUnknownRepo):
		writeError(w, http.StatusConflict, "not on the board")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "launch failed")
		return
	}
	writeJSON(w, res)
}

func (h *PipelineHandler) clearLock(w http.ResponseWriter, r *http.Request) {
	req, ok := h.stuckRequest(w, r)
	if !ok {
		return
	}
	h.exclusive(w, req, func() {
		if h.clear(w, r, req) {
			writeJSON(w, map[string]bool{"cleared": true})
		}
	})
}

func (h *PipelineHandler) clearAndReenrich(w http.ResponseWriter, r *http.Request) {
	req, ok := h.stuckRequest(w, r)
	if !ok {
		return
	}
	h.exclusive(w, req, func() {
		if h.clear(w, r, req) {
			h.writeLaunch(w, r, h.Enrich, req)
		}
	})
}

// stuckRequest decodes the body and requires the issue to be a Running
// enrichment card unchanged for longer than StuckEnrichingAfter.
func (h *PipelineHandler) stuckRequest(w http.ResponseWriter, r *http.Request) (LaunchRequest, bool) {
	req, ok := decodeLaunchRequest(w, r)
	if !ok {
		return req, false
	}
	for _, c := range h.Board().Running {
		if c.Kind != cockpit.KindEnrichment || c.Repo != req.Repo || c.Issue != req.Number {
			continue
		}
		if h.now().Sub(c.Since) <= StuckEnrichingAfter {
			writeError(w, http.StatusConflict, "not stuck")
			return req, false
		}
		return req, true
	}
	writeError(w, http.StatusConflict, "not on the board")
	return req, false
}

// clear runs ClearLock and writes the error response itself; it reports whether
// the lock is gone.
func (h *PipelineHandler) clear(w http.ResponseWriter, r *http.Request, req LaunchRequest) bool {
	err := h.ClearLock(r.Context(), req)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrUnsupportedForge):
		writeError(w, http.StatusUnprocessableEntity, ErrUnsupportedForge.Error())
	case errors.Is(err, ErrNoToken):
		writeError(w, http.StatusUnprocessableEntity, ErrNoToken.Error())
	case errors.Is(err, ErrUnknownRepo):
		writeError(w, http.StatusConflict, "not on the board")
	default:
		writeError(w, http.StatusBadGateway, "forge call failed")
	}
	return false
}

func (h *PipelineHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
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
