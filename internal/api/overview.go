package api

import (
	"context"
	"net/http"

	"github.com/freaxnx01/bridge/internal/overview"
)

// OverviewHandler handles GET /api/overview.
type OverviewHandler struct {
	Build func(ctx context.Context) (overview.Snapshot, error)
}

func (h *OverviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snap, err := h.Build(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, snap)
}
