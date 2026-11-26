package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freaxnx01/bridge/internal/cockpit"
)

// diffHandler serves /api/inbox/diff with a Diff func that records what it was
// asked for and answers with the given diff and error.
func diffHandler(d cockpit.Diff, err error, asked *string) http.Handler {
	return &DiffHandler{
		Diff: func(_ context.Context, owner, repo string, number int) (cockpit.Diff, error) {
			if asked != nil {
				*asked = fmt.Sprintf("%s/%s#%d", owner, repo, number)
			}
			return d, err
		},
	}
}

var sampleDiff = cockpit.Diff{
	PR: 9, HeadSHA: mergeHead,
	Files: []cockpit.DiffFile{{Path: "a.go", Status: "modified", Additions: 1, Patch: "@@ -1 +1 @@\n+x"}},
}

func callDiff(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestDiff_StatusMapping_Table(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		err        error
		wantStatus int
		wantError  string
	}{
		{"ok", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5", nil, http.StatusOK, ""},
		{"not a merge card", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5",
			fmt.Errorf("%w: o/a#5", cockpit.ErrNotCandidate), http.StatusConflict, "not on the inbox"},
		{"repo not wired", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5",
			fmt.Errorf("%w: o/a", ErrUnknownRepo), http.StatusConflict, "not on the inbox"},
		{"head moved since the card", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5",
			fmt.Errorf("%w: o/a#9 is at abc", cockpit.ErrHeadMoved), http.StatusConflict, "head moved"},
		{"no token for the owner", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5",
			fmt.Errorf("%w: boom", ErrNoToken), http.StatusUnprocessableEntity, ErrNoToken.Error()},
		{"forge cannot be read for diffs", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5",
			fmt.Errorf("%w: forgejo", ErrMergeUnsupported), http.StatusUnprocessableEntity, ErrMergeUnsupported.Error()},
		{"forge fails", http.MethodGet, "/api/inbox/diff?repo=o/a&number=5",
			errors.New("403 token xyz"), http.StatusBadGateway, "forge call failed"},
		{"wrong method", http.MethodPost, "/api/inbox/diff?repo=o/a&number=5",
			nil, http.StatusMethodNotAllowed, "method not allowed"},
		{"repo without a slash", http.MethodGet, "/api/inbox/diff?repo=x&number=5",
			nil, http.StatusBadRequest, "repo must be owner/name"},
		{"repo missing", http.MethodGet, "/api/inbox/diff?number=5",
			nil, http.StatusBadRequest, "repo must be owner/name"},
		{"number zero", http.MethodGet, "/api/inbox/diff?repo=o/a&number=0",
			nil, http.StatusBadRequest, "number must be positive"},
		{"number not a number", http.MethodGet, "/api/inbox/diff?repo=o/a&number=abc",
			nil, http.StatusBadRequest, "number must be positive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := callDiff(diffHandler(sampleDiff, tc.err, nil), tc.method, tc.target)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantError == "" {
				var got cockpit.Diff
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.PR != 9 || got.HeadSHA != mergeHead || len(got.Files) != 1 {
					t.Errorf("body = %s", rec.Body)
				}
				return
			}
			var got errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Error != tc.wantError {
				t.Errorf("error = %q, want %q", got.Error, tc.wantError)
			}
		})
	}
}

func TestDiff_WrongMethod_AnnouncesGET(t *testing.T) {
	rec := callDiff(diffHandler(sampleDiff, nil, nil), http.MethodPost, "/api/inbox/diff?repo=o/a&number=5")
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want GET", got)
	}
}

func TestDiff_SplitsTheRepoForTheLoader(t *testing.T) {
	var asked string
	rec := callDiff(diffHandler(sampleDiff, nil, &asked), http.MethodGet, "/api/inbox/diff?repo=o/a&number=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if asked != "o/a#5" {
		t.Errorf("asked = %q, want o/a#5", asked)
	}
}

// A bad request must never reach the forge: the guard is the whole point of
// answering 400 before the loader runs.
func TestDiff_InvalidRequest_NeverCallsTheLoader(t *testing.T) {
	var asked string
	rec := callDiff(diffHandler(sampleDiff, nil, &asked), http.MethodGet, "/api/inbox/diff?repo=o&number=5")
	if rec.Code != http.StatusBadRequest || asked != "" {
		t.Errorf("status = %d, asked = %q, want 400 and no load", rec.Code, asked)
	}
}

func TestDiff_EmptyFileList_SerializesAsArray(t *testing.T) {
	rec := callDiff(diffHandler(cockpit.Diff{PR: 9, HeadSHA: mergeHead}, nil, nil),
		http.MethodGet, "/api/inbox/diff?repo=o/a&number=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if !jsonHasEmptyFiles(t, rec.Body.Bytes()) {
		t.Errorf("body = %s, want files []", rec.Body)
	}
}

func jsonHasEmptyFiles(t *testing.T, raw []byte) bool {
	t.Helper()
	var got struct {
		Files []cockpit.DiffFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got.Files != nil && len(got.Files) == 0
}
