package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freaxnx01/bridge/internal/agentview"
)

func TestAgentsHandler_Success(t *testing.T) {
	h := &AgentsHandler{List: func(_ context.Context) ([]agentview.Session, error) {
		return []agentview.Session{{Name: "s1", Status: "busy", Kind: "interactive", CWD: "/x"}}, nil
	}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rr.Code)
	}
	var got []agentview.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "s1" {
		t.Errorf("body = %+v", got)
	}
}

func TestAgentsHandler_Unavailable_ReturnsEmpty200(t *testing.T) {
	h := &AgentsHandler{List: func(_ context.Context) ([]agentview.Session, error) {
		return nil, agentview.ErrUnavailable
	}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rr.Code)
	}
	var got []agentview.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty array, got %+v", got)
	}
}

func TestAgentsHandler_MethodNotAllowed(t *testing.T) {
	h := &AgentsHandler{List: func(_ context.Context) ([]agentview.Session, error) { return nil, nil }}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/agents", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", rr.Code)
	}
}
