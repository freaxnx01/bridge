package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mergeHead = "96fae5fd5d9aa4022a3b5a57a69b50510a563891"

// mergeHandler serves /api/inbox/merge with a Merge func that records its
// request and answers with the given result and error.
func mergeHandler(res MergeResult, err error, got *MergeRequest) http.Handler {
	h := &InboxHandler{
		Merge: func(_ context.Context, req MergeRequest) (MergeResult, error) {
			if got != nil {
				*got = req
			}
			return res, err
		},
	}
	return h.MergeHandler()
}

func postMerge(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/inbox/merge", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// validBody is a well-formed merge request for o/a#5's PR 9.
const validBody = `{"repo":"o/a","number":5,"pr":9,"headSha":"` + mergeHead + `"}`

func TestMergeHandler_StatusMapping_Table(t *testing.T) {
	tests := []struct {
		name       string
		result     MergeResult
		err        error
		wantStatus int
		wantError  string
	}{
		{"merged", MergeResult{Merged: true, BranchDeleted: true}, nil, http.StatusOK, ""},
		{"not mergeable", MergeResult{}, ErrNotMergeable{Reason: "head moved"},
			http.StatusConflict, "head moved"},
		{"not on the inbox", MergeResult{}, ErrNotMergeable{Reason: "not on the inbox"},
			http.StatusConflict, "not on the inbox"},
		{"forge refuses with 403", MergeResult{}, ErrForgeRefused{Status: 403, Message: "Resource not accessible"},
			http.StatusForbidden, "Resource not accessible"},
		{"forge refuses with 405", MergeResult{}, ErrForgeRefused{Status: 405, Message: "Required status check"},
			http.StatusConflict, "Required status check"},
		{"forge refuses with 409", MergeResult{}, ErrForgeRefused{Status: 409, Message: "Head branch was modified"},
			http.StatusConflict, "Head branch was modified"},
		{"forge refuses with 422", MergeResult{}, ErrForgeRefused{Status: 422, Message: "Validation failed"},
			http.StatusConflict, "Validation failed"},
		{"forge fails otherwise", MergeResult{}, ErrForgeRefused{Status: 500, Message: "Server Error"},
			http.StatusBadGateway, "forge call failed"},
		{"unknown repo", MergeResult{}, ErrUnknownRepo, http.StatusConflict, "not on the inbox"},
		{"no token for the owner", MergeResult{}, ErrNoToken, http.StatusUnprocessableEntity, ErrNoToken.Error()},
		{"forge cannot merge", MergeResult{}, ErrMergeUnsupported,
			http.StatusUnprocessableEntity, "merge is not supported for this forge"},
		{"anything else", MergeResult{}, errors.New("boom"), http.StatusBadGateway, "forge call failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := postMerge(t, mergeHandler(tc.result, tc.err, nil), validBody)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantError == "" {
				var res MergeResult
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatal(err)
				}
				if res != tc.result {
					t.Errorf("result = %+v, want %+v", res, tc.result)
				}
				return
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error != tc.wantError {
				t.Errorf("error = %q, want %q", body.Error, tc.wantError)
			}
		})
	}
}

func TestMergeHandler_PassesTheRequestThrough(t *testing.T) {
	var got MergeRequest
	rec := postMerge(t, mergeHandler(MergeResult{Merged: true, BranchDeleted: true}, nil, &got), validBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	want := MergeRequest{Repo: "o/a", Number: 5, PR: 9, HeadSHA: mergeHead}
	if got != want {
		t.Errorf("request = %+v, want %+v", got, want)
	}
}

func TestMergeHandler_BranchNotDeleted_StillOK(t *testing.T) {
	res := MergeResult{Merged: true, BranchDeleted: false, BranchError: "protected"}
	rec := postMerge(t, mergeHandler(res, nil, nil), validBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"branchError":"protected"`) {
		t.Errorf("body = %s", rec.Body)
	}
}

func TestMergeHandler_BadInput_400_Table(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"short head sha", `{"repo":"o/a","number":5,"pr":9,"headSha":"abc"}`},
		{"non-hex head sha", `{"repo":"o/a","number":5,"pr":9,"headSha":"` + strings.Repeat("z", 40) + `"}`},
		{"missing head sha", `{"repo":"o/a","number":5,"pr":9}`},
		{"no pr", `{"repo":"o/a","number":5,"pr":0,"headSha":"` + mergeHead + `"}`},
		{"no issue number", `{"repo":"o/a","number":0,"pr":9,"headSha":"` + mergeHead + `"}`},
		{"repo without owner", `{"repo":"a","number":5,"pr":9,"headSha":"` + mergeHead + `"}`},
		{"invalid json", `{`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := (&InboxHandler{Merge: func(context.Context, MergeRequest) (MergeResult, error) {
				called = true
				return MergeResult{}, nil
			}}).MergeHandler()

			rec := postMerge(t, h, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
			if called {
				t.Error("the merge ran on an invalid request")
			}
		})
	}
}

func TestMergeHandler_UppercaseHeadSHA_Accepted(t *testing.T) {
	body := `{"repo":"o/a","number":5,"pr":9,"headSha":"` + strings.ToUpper(mergeHead) + `"}`
	rec := postMerge(t, mergeHandler(MergeResult{Merged: true, BranchDeleted: true}, nil, nil), body)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
}

func TestMergeHandler_GET_405(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox/merge", nil)
	rec := httptest.NewRecorder()
	mergeHandler(MergeResult{}, nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want POST", got)
	}
}

// The read-only inbox routes must not grow a write path: a POST that reaches
// ServeHTTP is still refused there.
func TestInboxHandler_ServeHTTP_RefusesPost(t *testing.T) {
	h := &InboxHandler{Inbox: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/inbox/merge", strings.NewReader(validBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — /api/inbox/merge is not a detail path", rec.Code)
	}
}
