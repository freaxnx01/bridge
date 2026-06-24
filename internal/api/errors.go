package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/freaxnx01/bridge/internal/forge"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort; client disconnect is benign
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg}) //nolint:errcheck // best-effort; client disconnect is benign
}

func httpStatus(err error) int {
	if errors.Is(err, forge.ErrRepoExists) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
