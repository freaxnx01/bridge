package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/cockpit"
)

type launchCall struct {
	kind string
	req  LaunchRequest
}

func newPipelineFixture(result LaunchResult) (*PipelineHandler, *[]launchCall) {
	var calls []launchCall
	h := &PipelineHandler{
		Board: func() cockpit.Board {
			return cockpit.Board{
				Ready: []cockpit.IssueCard{{Forge: "github", Repo: "o/r", Number: 5, Title: "ready"}},
				NeedsEnrichment: []cockpit.IssueCard{
					{Forge: "github", Repo: "o/r", Number: 7, Title: "gh"},
					{Forge: "forgejo", Repo: "o/f", Number: 3, Title: "fj"},
				},
			}
		},
		Refresh: func(context.Context) { calls = append(calls, launchCall{kind: "refresh"}) },
		Enrich: func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
			calls = append(calls, launchCall{"enrich", req})
			return result, nil
		},
		Dispatch: func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
			calls = append(calls, launchCall{"dispatch", req})
			return result, nil
		},
	}
	return h, &calls
}

func doReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPipelineHandler_GetBoard_ReturnsJSON(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	w := doReq(h, http.MethodGet, "/api/pipeline", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"running", "ready", "held", "needsEnrichment", "dispatchPaused", "collectedAt", "intervalsSeconds", "errors"} {
		if _, ok := got[k]; !ok {
			t.Errorf("key %q missing in %s", k, w.Body)
		}
	}
}

func TestPipelineHandler_EnrichNotOnBoard_409(t *testing.T) {
	h, calls := newPipelineFixture(LaunchResult{})
	w := doReq(h, http.MethodPost, "/api/pipeline/enrich", `{"repo":"o/r","number":99}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not on the board") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if len(*calls) != 0 {
		t.Errorf("must not launch: %v", *calls)
	}
}

func TestPipelineHandler_EnrichForgejoIssue_422(t *testing.T) {
	h, calls := newPipelineFixture(LaunchResult{})
	w := doReq(h, http.MethodPost, "/api/pipeline/enrich", `{"repo":"o/f","number":3}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "headless enrich is GitHub-only") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if len(*calls) != 0 {
		t.Errorf("must not launch: %v", *calls)
	}
}

func TestPipelineHandler_EnrichGithubIssue_CallsEnrich(t *testing.T) {
	h, calls := newPipelineFixture(LaunchResult{Slot: "r-enrich-7"})
	w := doReq(h, http.MethodPost, "/api/pipeline/enrich", `{"repo":"o/r","number":7}`)
	if w.Code != http.StatusOK || len(*calls) != 1 || (*calls)[0].kind != "enrich" {
		t.Fatalf("got %d %s calls=%v", w.Code, w.Body, *calls)
	}
}

func TestPipelineHandler_DispatchReadyIssue_CallsDispatch(t *testing.T) {
	h, calls := newPipelineFixture(LaunchResult{Slot: "r-dispatch-5", RCURL: "https://claude.ai/code/session_X"})
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	if len(*calls) != 1 || (*calls)[0].kind != "dispatch" || (*calls)[0].req != (LaunchRequest{Repo: "o/r", Number: 5}) {
		t.Fatalf("calls = %v", *calls)
	}
	var res LaunchResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Slot != "r-dispatch-5" || res.RCURL == "" {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(w.Body.String(), `"alreadyRunning":false`) || !strings.Contains(w.Body.String(), `"rcUrl"`) {
		t.Errorf("camelCase keys expected: %s", w.Body)
	}
}

func TestPipelineHandler_DispatchNotReady_409(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":7}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestPipelineHandler_BadNumber_400(t *testing.T) {
	for _, body := range []string{
		`{"repo":"o/r","number":0}`,
		`{"repo":"o/r","number":-1}`,
		`{"repo":"nosplit","number":5}`,
		`{"repo":"a/b/c","number":5}`,
		`not json`,
	} {
		h, calls := newPipelineFixture(LaunchResult{})
		w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
		if len(*calls) != 0 {
			t.Errorf("%s: must not launch", body)
		}
	}
}

func TestPipelineHandler_LaunchAlreadyRunning_Returns200WithFlag(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{Slot: "r-dispatch-5", AlreadyRunning: true})
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"alreadyRunning":true`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestPipelineHandler_AmbiguousRepo_409(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	h.Dispatch = func(context.Context, LaunchRequest) (LaunchResult, error) {
		return LaunchResult{}, ErrAmbiguousRepo
	}
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ambiguous repo name") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestPipelineHandler_Refresh_Returns202(t *testing.T) {
	h, calls := newPipelineFixture(LaunchResult{})
	w := doReq(h, http.MethodPost, "/api/pipeline/refresh", "")
	if w.Code != http.StatusAccepted || len(*calls) != 1 || (*calls)[0].kind != "refresh" {
		t.Fatalf("got %d calls=%v", w.Code, *calls)
	}
}

func TestPipelineHandler_WrongMethod_405(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/pipeline"},
		{http.MethodGet, "/api/pipeline/enrich"},
		{http.MethodGet, "/api/pipeline/dispatch"},
		{http.MethodGet, "/api/pipeline/refresh"},
		{http.MethodDelete, "/api/pipeline"},
	} {
		if w := doReq(h, tc.method, tc.path, ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tc.method, tc.path, w.Code)
		}
	}
}

func TestPipelineHandler_UnknownPath_404(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	if w := doReq(h, http.MethodGet, "/api/pipeline/nope", ""); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestPipelineHandler_EmptyBoard_SerializesEmptyArrays(t *testing.T) {
	h := &PipelineHandler{Board: func() cockpit.Board { return cockpit.Board{} }}
	w := doReq(h, http.MethodGet, "/api/pipeline", "")
	for _, k := range []string{`"running":[]`, `"ready":[]`, `"held":[]`, `"needsEnrichment":[]`, `"errors":[]`} {
		if !strings.Contains(w.Body.String(), k) {
			t.Errorf("%s missing in %s", k, w.Body)
		}
	}
}

func TestPipelineHandler_UnknownRepo_409NotOnBoard(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	h.Dispatch = func(context.Context, LaunchRequest) (LaunchResult, error) {
		return LaunchResult{}, fmt.Errorf("%w: o/r", ErrUnknownRepo)
	}
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not on the board") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestPipelineHandler_LaunchFails_500GenericBody(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	h.Dispatch = func(context.Context, LaunchRequest) (LaunchResult, error) {
		return LaunchResult{}, errors.New("bridge launch: exit 1: secret stderr /home/x")
	}
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "launch failed") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestPipelineHandler_ConcurrentSameIssue_SecondGets409(t *testing.T) {
	h, _ := newPipelineFixture(LaunchResult{})
	started, release := make(chan struct{}), make(chan struct{})
	h.Dispatch = func(context.Context, LaunchRequest) (LaunchResult, error) {
		close(started)
		<-release
		return LaunchResult{Slot: "s"}, nil
	}
	first := make(chan int)
	go func() { first <- doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`).Code }()
	<-started
	w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "launch already in progress") {
		t.Fatalf("second: %d %s", w.Code, w.Body)
	}
	close(release)
	if code := <-first; code != http.StatusOK {
		t.Errorf("first = %d, want 200", code)
	}
	// Guard released: a later request goes through again.
	h.Dispatch = func(context.Context, LaunchRequest) (LaunchResult, error) { return LaunchResult{Slot: "s"}, nil }
	if w := doReq(h, http.MethodPost, "/api/pipeline/dispatch", `{"repo":"o/r","number":5}`); w.Code != http.StatusOK {
		t.Errorf("after release: %d", w.Code)
	}
}
