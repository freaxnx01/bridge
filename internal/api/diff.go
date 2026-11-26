package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/freaxnx01/bridge/internal/cockpit"
)

// DiffHandler serves GET /api/inbox/diff: the read-only per-file diff of the
// PR on an issue's merge card. It is its own handler, not part of the open
// InboxHandler, because it is the one cockpit read that serves source code and
// therefore sits behind the bearer token.
type DiffHandler struct {
	Diff func(ctx context.Context, owner, repo string, number int) (cockpit.Diff, error)
}

func (h *DiffHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	owner, repo, ok := splitRepo(r.URL.Query().Get("repo"))
	if !ok {
		writeError(w, http.StatusBadRequest, "repo must be owner/name")
		return
	}
	number, err := strconv.Atoi(r.URL.Query().Get("number"))
	if err != nil || number <= 0 {
		writeError(w, http.StatusBadRequest, "number must be positive")
		return
	}
	d, err := h.Diff(r.Context(), owner, repo, number)
	status, message, refused := diffRefusalStatus(err)
	switch {
	case refused:
		writeError(w, status, message)
	case err != nil:
		// The forge's own message can name a repo the reader may not see, so
		// it goes to the log and never to the client.
		slog.Error("inbox diff failed", "repo", owner+"/"+repo, "number", number, "error", err)
		writeError(w, http.StatusBadGateway, "forge call failed")
	default:
		writeJSON(w, nonNilDiff(d))
	}
}

// diffRefusalStatus maps a diff load's refusals onto a status and the wording
// the card shows. The repo-level ones are the merge path's own: the diff
// resolves the card's repo and the owner's client the same way, so a repo
// unwired or a token gone since the card was collected cannot answer "forge
// call failed" here and "no token for this owner" there. ok is false for
// anything else, including a nil error.
func diffRefusalStatus(err error) (status int, message string, ok bool) {
	switch {
	case errors.Is(err, cockpit.ErrNotCandidate):
		return http.StatusConflict, "not on the inbox", true
	case errors.Is(err, cockpit.ErrHeadMoved):
		return http.StatusConflict, cockpit.ErrHeadMoved.Error(), true
	}
	return repoRefusalStatus(err)
}

// splitRepo parses an "owner/name" repo reference into its two non-empty
// halves.
func splitRepo(repo string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}

// repoIsOwnerName reports whether repo is a well-formed "owner/name".
func repoIsOwnerName(repo string) bool {
	_, _, ok := splitRepo(repo)
	return ok
}

// nonNilDiff makes an empty file list serialize as [] rather than null.
func nonNilDiff(d cockpit.Diff) cockpit.Diff {
	if d.Files == nil {
		d.Files = []cockpit.DiffFile{}
	}
	return d
}
