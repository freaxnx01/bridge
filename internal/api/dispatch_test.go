package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/dispatchrun"
)

var apiNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

// dispatchHandler records the `since` it was asked for, which is the only way
// to check that ?days= reaches the journal reader at all.
func dispatchHandler(recs []dispatch.JournalRecord, since *time.Time) *DispatchHandler {
	return &DispatchHandler{
		Ticks: func(s time.Time) ([]dispatch.JournalRecord, error) {
			if since != nil {
				*since = s
			}
			return recs, nil
		},
		Now: func() time.Time { return apiNow },
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestDispatchHistory_DefaultDays_Seven(t *testing.T) {
	var since time.Time

	if code := get(t, dispatchHandler(nil, &since), "/api/dispatch/history").Code; code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	if want := apiNow.AddDate(0, 0, -7); !since.Equal(want) {
		t.Errorf("since = %v, want %v", since, want)
	}
}

func TestDispatchHistory_Days_IsHonoured(t *testing.T) {
	var since time.Time

	get(t, dispatchHandler(nil, &since), "/api/dispatch/history?days=30")

	if want := apiNow.AddDate(0, 0, -30); !since.Equal(want) {
		t.Errorf("since = %v, want %v", since, want)
	}
}

func TestDispatchHistory_NoTicks_EmptyArrays(t *testing.T) {
	rec := get(t, dispatchHandler(nil, nil), "/api/dispatch/history")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"summary":[],"ticks":[]}` {
		t.Errorf("body = %s", got)
	}
}

func TestDispatchHistory_BadDays_400(t *testing.T) {
	for _, days := range []string{"0", "31", "x", "-1"} {
		t.Run(days, func(t *testing.T) {
			rec := get(t, dispatchHandler(nil, nil), "/api/dispatch/history?days="+days)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d", rec.Code)
			}
		})
	}
}

func TestDispatchHistory_ReaderFails_502(t *testing.T) {
	h := &DispatchHandler{
		Ticks: func(time.Time) ([]dispatch.JournalRecord, error) { return nil, errReadJournal },
		Now:   func() time.Time { return apiNow },
	}

	if code := get(t, h, "/api/dispatch/history").Code; code != http.StatusBadGateway {
		t.Errorf("status = %d", code)
	}
}

func TestDispatchHistory_Summary_IsServed(t *testing.T) {
	at := apiNow.Add(-time.Hour)
	recs := []dispatch.JournalRecord{{
		V: dispatch.JournalVersion, At: at, Trigger: dispatch.TriggerTimer, Mode: dispatch.ModeDryRun,
		Outcome: dispatch.OutcomeDecided,
		Decisions: []dispatch.JournalDecision{
			{Repo: "bridge", Issue: 41, Title: "t", Decision: dispatch.DecisionSkipped, Reason: "global cap 3/3"},
		},
	}}

	rec := get(t, dispatchHandler(recs, nil), "/api/dispatch/history?days=1")

	var got dispatchrun.History
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Summary) != 1 || got.Summary[0].Issue != 41 || got.Summary[0].TopReason != "global cap 3/3" {
		t.Errorf("summary = %+v", got.Summary)
	}
	if len(got.Ticks) != 1 || !got.Ticks[0].At.Equal(at) {
		t.Errorf("ticks = %+v", got.Ticks)
	}
}

func TestDispatchTick_Found_200(t *testing.T) {
	at := apiNow.Add(-2 * time.Hour)
	recs := []dispatch.JournalRecord{
		{V: 1, At: at.Add(-time.Hour), Trigger: dispatch.TriggerTimer},
		{V: 1, At: at, Trigger: dispatch.TriggerTimer, Outcome: dispatch.OutcomeDecided},
	}

	rec := get(t, dispatchHandler(recs, nil), "/api/dispatch/ticks/"+url.PathEscape(at.Format(time.RFC3339)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got dispatch.JournalRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.At.Equal(at) || got.Outcome != dispatch.OutcomeDecided {
		t.Errorf("got %+v", got)
	}
}

func TestDispatchTick_Unknown_404(t *testing.T) {
	at := apiNow.Add(-2 * time.Hour)

	rec := get(t, dispatchHandler(nil, nil), "/api/dispatch/ticks/"+url.PathEscape(at.Format(time.RFC3339)))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestDispatchTick_BadTimestamp_400(t *testing.T) {
	rec := get(t, dispatchHandler(nil, nil), "/api/dispatch/ticks/yesterday")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestDispatch_UnknownPath_404(t *testing.T) {
	if code := get(t, dispatchHandler(nil, nil), "/api/dispatch/nope").Code; code != http.StatusNotFound {
		t.Errorf("status = %d", code)
	}
}

func TestDispatch_Post_405(t *testing.T) {
	rec := httptest.NewRecorder()
	dispatchHandler(nil, nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/dispatch/history", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Header().Get("Allow") != http.MethodGet {
		t.Errorf("Allow = %q", rec.Header().Get("Allow"))
	}
}

var errReadJournal = errors.New("read journal dir: permission denied")
