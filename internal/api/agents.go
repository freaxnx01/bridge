package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/freaxnx01/bridge/internal/agentview"
)

// AgentsHandler handles GET /api/agents: the live Claude sessions as JSON. When
// the claude CLI is unavailable it returns an empty array (200) so the WebUI shows
// an empty section rather than an error.
type AgentsHandler struct {
	List func(ctx context.Context) ([]agentview.Session, error)
}

func (h *AgentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sessions, err := h.List(r.Context())
	if err != nil {
		if errors.Is(err, agentview.ErrUnavailable) {
			writeJSON(w, []agentview.Session{})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, sessions)
}
