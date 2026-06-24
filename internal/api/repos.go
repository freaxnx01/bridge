package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// RepoDetail is the payload for GET /api/repos/{owner}/{name}.
type RepoDetail struct {
	Repo     core.Repo      `json:"repo"`
	Sessions []core.Session `json:"sessions"`
	Issues   []forge.Issue  `json:"issues"`
}

// ReposHandler handles /api/repos and /api/repos/{owner}/{name}.
type ReposHandler struct {
	Discover func() ([]core.Repo, error)
	Issues   func(ctx context.Context, forge, owner, repo string) ([]forge.Issue, error)
	Create   func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error)
	Notify   func(eventType string, data any) // nil = no-op
}

func (h *ReposHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/repos")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		h.list(w, r)
	case path == "" && r.Method == http.MethodPost:
		h.create(w, r)
	case path == "":
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "path must be /api/repos/{owner}/{name}")
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.detail(w, r, parts[0], parts[1])
	}
}

func (h *ReposHandler) list(w http.ResponseWriter, r *http.Request) {
	repos, err := h.Discover()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, repos)
}

func (h *ReposHandler) detail(w http.ResponseWriter, r *http.Request, owner, name string) {
	repos, err := h.Discover()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var repo *core.Repo
	for i := range repos {
		if strings.EqualFold(repos[i].Owner, owner) && strings.EqualFold(repos[i].Name, name) {
			repo = &repos[i]
			break
		}
	}
	if repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	// Best-effort: empty if tmux is not running.
	sessions, _ := core.LiveSessions()
	var repoSessions []core.Session
	for _, s := range sessions {
		if strings.Contains(s.TmuxName, repo.Name) {
			repoSessions = append(repoSessions, s)
		}
	}
	var issues []forge.Issue
	if h.Issues != nil {
		issues, _ = h.Issues(r.Context(), repo.Forge, repo.Owner, repo.Name)
	}
	writeJSON(w, RepoDetail{Repo: *repo, Sessions: repoSessions, Issues: issues})
}

type createRepoRequest struct {
	Name    string `json:"name"`
	Forge   string `json:"forge"`
	Private bool   `json:"private"`
}

func (h *ReposHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.Create == nil {
		writeError(w, http.StatusNotImplemented, "create not configured")
		return
	}
	var req createRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	repo, err := h.Create(r.Context(), req.Name, req.Forge, req.Private)
	if err != nil {
		writeError(w, httpStatus(err), err.Error())
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(repo) //nolint:errcheck // best-effort; client disconnect is benign
}
