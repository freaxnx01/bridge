package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/dispatch"
)

// InboxHandler serves the read-only inbox: GET /api/inbox and
// GET /api/inbox/{owner}/{repo}/{number}.
type InboxHandler struct {
	Inbox  func() cockpit.Inbox
	Detail func(ctx context.Context, owner, repo string, number int) (cockpit.IssueDetail, error)
}

func (h *InboxHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/inbox" {
		if !allowGet(w, r) {
			return
		}
		writeJSON(w, nonNilInbox(h.Inbox()))
		return
	}
	owner, repo, rawNumber, ok := splitDetailPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !allowGet(w, r) {
		return
	}
	number, err := strconv.Atoi(rawNumber)
	if err != nil || number <= 0 {
		writeError(w, http.StatusBadRequest, "number must be positive")
		return
	}
	h.detail(w, r, owner, repo, number)
}

func (h *InboxHandler) detail(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	d, err := h.Detail(r.Context(), owner, repo, number)
	switch {
	case errors.Is(err, cockpit.ErrUnknownRepo), errors.Is(err, cockpit.ErrNotCandidate):
		writeError(w, http.StatusNotFound, "not found")
	case err != nil:
		// The forge's own message can carry a token or a repo a reader may not
		// see, so it goes to the log and never to the client.
		slog.Error("inbox detail failed", "repo", owner+"/"+repo, "number", number, "error", err)
		writeError(w, http.StatusBadGateway, "forge request failed")
	default:
		writeJSON(w, nonNilDetail(d))
	}
}

// splitDetailPath parses /api/inbox/{owner}/{repo}/{number} into its exactly
// three non-empty segments.
func splitDetailPath(path string) (owner, repo, number string, ok bool) {
	rest, found := strings.CutPrefix(path, "/api/inbox/")
	if !found {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// allowGet answers anything but GET with 405 and reports whether to go on.
func allowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

// nonNilInbox makes empty sections serialize as [] rather than null.
func nonNilInbox(ib cockpit.Inbox) cockpit.Inbox {
	if ib.Cards == nil {
		ib.Cards = []cockpit.InboxCard{}
	}
	if ib.Waiting == nil {
		ib.Waiting = []cockpit.InboxCard{}
	}
	if ib.Parked == nil {
		ib.Parked = []cockpit.InboxCard{}
	}
	return ib
}

func nonNilDetail(d cockpit.IssueDetail) cockpit.IssueDetail {
	if d.Runs == nil {
		d.Runs = []dispatch.RunRecord{}
	}
	if d.Issue.Labels == nil {
		d.Issue.Labels = []string{}
	}
	return d
}
