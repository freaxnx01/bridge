package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freaxnx01/bridge/internal/overview"
)

func TestOverviewHandler_ReturnsSnapshot(t *testing.T) {
	want := overview.Snapshot{
		Ranked: []overview.RankedItem{{Title: "fix bug", Score: 3.5}},
	}
	h := &OverviewHandler{
		Build: func(_ context.Context) (overview.Snapshot, error) { return want, nil },
	}
	r := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got overview.Snapshot
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Ranked) != 1 || got.Ranked[0].Title != "fix bug" {
		t.Errorf("got Ranked = %+v", got.Ranked)
	}
}

func TestOverviewHandler_BuildError_Returns500(t *testing.T) {
	h := &OverviewHandler{
		Build: func(_ context.Context) (overview.Snapshot, error) {
			return overview.Snapshot{}, fmt.Errorf("forge down")
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestOverviewHandler_WrongMethod_Returns405(t *testing.T) {
	h := &OverviewHandler{
		Build: func(_ context.Context) (overview.Snapshot, error) { return overview.Snapshot{}, nil },
	}
	r := httptest.NewRequest(http.MethodPost, "/api/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
