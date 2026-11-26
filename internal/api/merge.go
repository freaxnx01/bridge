package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
)

// MergeRequest asks for one agent PR to be merged. HeadSHA is the head the
// client's card showed: the merge is pinned to it, so a push between render and
// click cannot land something the operator never saw.
type MergeRequest struct {
	Repo    string `json:"repo"` // "owner/name"
	Number  int    `json:"number"`
	PR      int    `json:"pr"`
	HeadSHA string `json:"headSha"`
	// BaseRef is the base branch the card showed. It is filled server-side
	// from the snapshot, never from the request body.
	BaseRef string `json:"-"`
}

// MergeResult is what a merge did. A branch that could not be deleted is
// reported, not an error: the merge itself landed.
type MergeResult struct {
	Merged        bool   `json:"merged"`
	BranchDeleted bool   `json:"branchDeleted"`
	BranchError   string `json:"branchError,omitempty"`
}

// ErrNotMergeable means bridge refused the merge itself, with Reason being the
// same wording the card shows ("head moved", "checks running", …).
type ErrNotMergeable struct{ Reason string }

func (e ErrNotMergeable) Error() string { return e.Reason }

// ErrForgeRefused means the forge refused the merge. Status and Message are
// its own: branch protection bridge cannot fully read is the forge's call, so
// its wording is what the operator needs to see.
type ErrForgeRefused struct {
	Status  int
	Message string
}

func (e ErrForgeRefused) Error() string { return e.Message }

// fullSHARE matches a full 40-hex commit SHA, the only head a merge may be
// pinned to: an abbreviated one could match more than one commit.
var fullSHARE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// MergeHandler serves POST /api/inbox/merge. It is a separate handler from the
// read-only ServeHTTP so the route can be wrapped in the write guard (token
// configured + bearer + same origin) without opening the GETs.
func (h *InboxHandler) MergeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		req, ok := decodeMergeRequest(w, r)
		if !ok {
			return
		}
		res, err := h.Merge(r.Context(), req)
		if err != nil {
			writeMergeError(w, req, err)
			return
		}
		writeJSON(w, res)
	})
}

// decodeMergeRequest validates everything the merge path takes from the client.
func decodeMergeRequest(w http.ResponseWriter, r *http.Request) (MergeRequest, bool) {
	var req MergeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLaunchBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return req, false
	}
	switch {
	case !repoIsOwnerName(req.Repo):
		writeError(w, http.StatusBadRequest, "repo must be owner/name")
	case req.Number <= 0:
		writeError(w, http.StatusBadRequest, "number must be positive")
	case req.PR <= 0:
		writeError(w, http.StatusBadRequest, "pr must be positive")
	case !fullSHARE.MatchString(req.HeadSHA):
		writeError(w, http.StatusBadRequest, "headSha must be a full 40-hex commit SHA")
	default:
		return req, true
	}
	return req, false
}

// writeMergeError maps a merge failure onto a status. Bridge's own refusals and
// the forge's are conflicts the operator can act on, so their reason is shown;
// anything else goes to the log and answers with a generic message.
func writeMergeError(w http.ResponseWriter, req MergeRequest, err error) {
	var notMergeable ErrNotMergeable
	var refused ErrForgeRefused
	status, message, repoRefused := repoRefusalStatus(err)
	switch {
	case errors.As(err, &notMergeable):
		writeError(w, http.StatusConflict, notMergeable.Reason)
	case errors.As(err, &refused):
		writeForgeRefusal(w, req, refused)
	case repoRefused:
		writeError(w, status, message)
	default:
		slog.Error("inbox merge failed", "repo", req.Repo, "pr", req.PR, "error", err)
		writeError(w, http.StatusBadGateway, "forge call failed")
	}
}

// repoRefusalStatus maps the refusals every card action shares — a repo that is
// no longer wired, an owner whose token is gone, a forge that cannot be merged
// — onto a status and the wording to show. Both the merge and the diff of a card
// resolve the same repo and the same client, so they answer these the same way.
// ok is false for anything else, including a nil error.
func repoRefusalStatus(err error) (status int, message string, ok bool) {
	switch {
	case errors.Is(err, ErrUnknownRepo):
		return http.StatusConflict, "not on the inbox", true
	case errors.Is(err, ErrNoToken):
		return http.StatusUnprocessableEntity, ErrNoToken.Error(), true
	case errors.Is(err, ErrMergeUnsupported):
		return http.StatusUnprocessableEntity, ErrMergeUnsupported.Error(), true
	}
	return 0, "", false
}

// writeForgeRefusal passes the forge's own refusal on: the statuses that mean
// "this PR may not be merged right now" become a 409, a permission refusal
// stays a 403, and anything else is a bad gateway.
func writeForgeRefusal(w http.ResponseWriter, req MergeRequest, refused ErrForgeRefused) {
	switch refused.Status {
	case http.StatusForbidden:
		writeError(w, http.StatusForbidden, refused.Message)
	case http.StatusMethodNotAllowed, http.StatusConflict, http.StatusUnprocessableEntity:
		writeError(w, http.StatusConflict, refused.Message)
	default:
		slog.Error("inbox merge refused", "repo", req.Repo, "pr", req.PR,
			"status", refused.Status, "message", refused.Message)
		writeError(w, http.StatusBadGateway, "forge call failed")
	}
}
