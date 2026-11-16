package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/dispatchrun"
)

// Bounds on ?days=: one day is the shortest useful review, and the journal's
// own retention is the longest answerable one.
const (
	minHistoryDays     = 1
	maxHistoryDays     = dispatch.JournalRetentionDays
	defaultHistoryDays = 7
)

// DispatchHandler serves the dispatcher's own history, read-only:
// GET /api/dispatch/history?days=N and GET /api/dispatch/ticks/{at}.
type DispatchHandler struct {
	Ticks func(since time.Time) ([]dispatch.JournalRecord, error)
	Now   func() time.Time // nil means time.Now
}

func (h *DispatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/dispatch/history":
		if allowGet(w, r) {
			h.history(w, r)
		}
	case strings.HasPrefix(r.URL.Path, "/api/dispatch/ticks/"):
		if allowGet(w, r) {
			h.tick(w, strings.TrimPrefix(r.URL.Path, "/api/dispatch/ticks/"))
		}
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *DispatchHandler) history(w http.ResponseWriter, r *http.Request) {
	days, ok := historyDays(r.URL.Query().Get("days"))
	if !ok {
		writeError(w, http.StatusBadRequest,
			"days must be between "+strconv.Itoa(minHistoryDays)+" and "+strconv.Itoa(maxHistoryDays))
		return
	}
	recs, err := h.read(h.now().AddDate(0, 0, -days))
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot read the dispatch journal")
		return
	}
	writeJSON(w, dispatchrun.Summarize(recs))
}

func (h *DispatchHandler) tick(w http.ResponseWriter, rawAt string) {
	at, err := parseTickAt(rawAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "at must be an RFC3339 timestamp")
		return
	}
	// The journal is keyed by time, so the record is at or after `at` by
	// construction; reading from `at` skips every earlier day file.
	recs, err := h.read(at)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot read the dispatch journal")
		return
	}
	for _, rec := range recs {
		if rec.At.Equal(at) {
			writeJSON(w, rec)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

// read wraps the journal reader so one place logs the real error: it names a
// filesystem path, which never belongs in a response body.
func (h *DispatchHandler) read(since time.Time) ([]dispatch.JournalRecord, error) {
	recs, err := h.Ticks(since)
	if err != nil {
		slog.Error("dispatch history read failed", "since", since, "error", err)
	}
	return recs, err
}

// historyDays parses ?days=, defaulting to a week when absent.
func historyDays(raw string) (int, bool) {
	if raw == "" {
		return defaultHistoryDays, true
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < minHistoryDays || days > maxHistoryDays {
		return 0, false
	}
	return days, true
}

// parseTickAt decodes the {at} segment. It is path-escaped by the client
// because an RFC3339 timestamp carries colons and a "+" offset.
func parseTickAt(raw string) (time.Time, error) {
	unescaped, err := url.PathUnescape(raw)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, unescaped)
}

func (h *DispatchHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
