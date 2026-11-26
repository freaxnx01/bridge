package forge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveJSON answers every request with body and records the last request URI.
func serveJSON(t *testing.T, body string, gotURI *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotURI = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGithubGetPullRequest_MapsSingleEndpointFields(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"number":90,"title":"t","body":"Closes #41","state":"open",
	  "head":{"ref":"ai/41","sha":"abc"},"base":{"ref":"main"},"user":{"login":"bot"},
	  "merged_at":null,"mergeable_state":"blocked","changed_files":3}`, &uri)

	pr, err := NewGithubClient("tok", srv.URL).GetPullRequest(context.Background(), "o", "r", 90)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/pulls/90" {
		t.Errorf("uri: %s", uri)
	}
	if pr.HeadSHA != "abc" || pr.MergeableState != "blocked" || pr.ChangedFiles != 3 || pr.Body != "Closes #41" {
		t.Errorf("pr: %+v", pr)
	}
}

func TestGithubListPullRequestFiles_MapsFilenameToPath(t *testing.T) {
	var uri string
	srv := serveJSON(t, `[{"filename":"a/b.go","additions":5,"deletions":2,"status":"modified"}]`, &uri)

	files, err := NewGithubClient("tok", srv.URL).ListPullRequestFiles(context.Background(), "o", "r", 90)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/pulls/90/files?per_page=100" {
		t.Errorf("uri: %s", uri)
	}
	if len(files) != 1 || files[0] != (PRFile{Path: "a/b.go", Status: "modified", Additions: 5, Deletions: 2}) {
		t.Errorf("files: %+v", files)
	}
}

func TestGithubListPullRequestFiles_StatusAndPatch(t *testing.T) {
	var uri string
	srv := serveJSON(t, `[{"filename":"a.go","status":"modified","additions":1,"deletions":0,
	    "patch":"@@ -1 +1 @@\n+x"},
	  {"filename":"img.png","status":"added","additions":0,"deletions":0}]`, &uri)

	files, err := NewGithubClient("tok", srv.URL).ListPullRequestFiles(context.Background(), "o", "r", 90)
	if err != nil {
		t.Fatal(err)
	}
	want := []PRFile{
		{Path: "a.go", Status: "modified", Additions: 1, Patch: "@@ -1 +1 @@\n+x"},
		{Path: "img.png", Status: "added"},
	}
	if len(files) != 2 || files[0] != want[0] || files[1] != want[1] {
		t.Errorf("files: %+v", files)
	}
}

func TestGithubListCheckRuns_MapsAppSlugAndNullConclusion(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"total_count":2,"check_runs":[
	  {"name":"build","status":"completed","conclusion":"failure","app":{"slug":"github-actions"}},
	  {"name":"lint","status":"in_progress","conclusion":null,"app":{"slug":"github-actions"}}]}`, &uri)

	runs, err := NewGithubClient("tok", srv.URL).ListCheckRuns(context.Background(), "o", "r", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/commits/abc/check-runs?per_page=100" {
		t.Errorf("uri: %s", uri)
	}
	want := []CheckRun{
		{Name: "build", Status: "completed", Conclusion: "failure", App: "github-actions"},
		{Name: "lint", Status: "in_progress", Conclusion: "", App: "github-actions"},
	}
	if len(runs) != 2 || runs[0] != want[0] || runs[1] != want[1] {
		t.Errorf("runs: %+v", runs)
	}
}

func TestGithubListCommitStatuses_UsesCombinedStatus(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"state":"pending","statuses":[
	  {"context":"ci/legacy","state":"success","description":"ok","target_url":"https://ci/1"}]}`, &uri)

	statuses, err := NewGithubClient("tok", srv.URL).ListCommitStatuses(context.Background(), "o", "r", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/commits/abc/status" {
		t.Errorf("uri: %s", uri)
	}
	want := CommitStatus{Context: "ci/legacy", State: "success", Description: "ok", URL: "https://ci/1"}
	if len(statuses) != 1 || statuses[0] != want {
		t.Errorf("statuses: %+v", statuses)
	}
}

func TestGithubListWorkflowRuns_FiltersAndMapsActors(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"total_count":1,"workflow_runs":[
	  {"name":"agent","event":"issues","status":"completed","conclusion":"success",
	   "display_title":"fix(nav): x",
	   "actor":{"login":"freaxnx01"},"triggering_actor":{"login":"github-actions[bot]"},
	   "head_branch":"ai/41","head_sha":"abc","created_at":"2026-10-01T10:00:00Z",
	   "html_url":"https://github.com/o/r/actions/runs/1"}]}`, &uri)

	runs, err := NewGithubClient("tok", srv.URL).ListWorkflowRuns(context.Background(), "o", "r", "ai/41", "abc", 20)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/actions/runs?branch=ai%2F41&head_sha=abc&per_page=20" {
		t.Errorf("uri: %s", uri)
	}
	if len(runs) != 1 || runs[0].Actor != "freaxnx01" || runs[0].TriggeringActor != "github-actions[bot]" {
		t.Fatalf("runs: %+v", runs)
	}
	if runs[0].Name != "agent" || runs[0].Event != "issues" || runs[0].HeadBranch != "ai/41" || runs[0].Created.IsZero() {
		t.Errorf("run fields: %+v", runs[0])
	}
	if runs[0].DisplayTitle != "fix(nav): x" {
		t.Errorf("DisplayTitle = %q, want %q", runs[0].DisplayTitle, "fix(nav): x")
	}
}

func TestGithubListWorkflowRuns_OmitsEmptyFilters(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"total_count":0,"workflow_runs":[]}`, &uri)

	if _, err := NewGithubClient("tok", srv.URL).ListWorkflowRuns(context.Background(), "o", "r", "", "", 5); err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/actions/runs?per_page=5" {
		t.Errorf("uri: %s", uri)
	}
}

// recorder answers status/body and records the request's method, URI and body.
type recorder struct {
	method string
	uri    string
	body   string
}

func serveRecorded(t *testing.T, status int, body string, rec *recorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.method, rec.uri, rec.body = r.Method, r.URL.RequestURI(), string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMergePullRequest_SendsSquashAndSHA(t *testing.T) {
	var rec recorder
	srv := serveRecorded(t, http.StatusOK, `{"merged":true,"sha":"abc"}`, &rec)

	err := NewGithubClient("tok", srv.URL).MergePullRequest(context.Background(), "o", "r", 7, "abc", "squash")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "PUT" || rec.uri != "/repos/o/r/pulls/7/merge" {
		t.Errorf("request = %s %s", rec.method, rec.uri)
	}
	if got := strings.TrimSpace(rec.body); got != `{"merge_method":"squash","sha":"abc"}` {
		t.Errorf("body = %s", got)
	}
}

func TestMergePullRequest_Refused_ReturnsAPIError(t *testing.T) {
	var rec recorder
	srv := serveRecorded(t, http.StatusMethodNotAllowed,
		`{"message":"Required status check \"build\" is expected."}`, &rec)

	err := NewGithubClient("tok", srv.URL).MergePullRequest(context.Background(), "o", "r", 7, "abc", "squash")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", apiErr.Status)
	}
	if apiErr.Message != `Required status check "build" is expected.` {
		t.Errorf("message = %q", apiErr.Message)
	}
}

func TestMergePullRequest_NonJSONError_KeepsRawBody(t *testing.T) {
	var rec recorder
	srv := serveRecorded(t, http.StatusBadGateway, "upstream exploded", &rec)

	err := NewGithubClient("tok", srv.URL).MergePullRequest(context.Background(), "o", "r", 7, "abc", "squash")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "upstream exploded" {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteBranch_NestedRef_EscapesSegments(t *testing.T) {
	var rec recorder
	srv := serveRecorded(t, http.StatusNoContent, "", &rec)

	if err := NewGithubClient("tok", srv.URL).DeleteBranch(context.Background(), "o", "r", "feat/382-merge"); err != nil {
		t.Fatal(err)
	}
	if rec.method != "DELETE" || rec.uri != "/repos/o/r/git/refs/heads/feat/382-merge" {
		t.Errorf("request = %s %s", rec.method, rec.uri)
	}
}

func TestDeleteBranch_AlreadyGone_Nil(t *testing.T) {
	var rec recorder
	srv := serveRecorded(t, http.StatusUnprocessableEntity, `{"message":"Reference does not exist"}`, &rec)

	if err := NewGithubClient("tok", srv.URL).DeleteBranch(context.Background(), "o", "r", "gone"); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestDeleteBranch_OtherFailure_ReturnsAPIError(t *testing.T) {
	var rec recorder
	srv := serveRecorded(t, http.StatusForbidden, `{"message":"Protected branch"}`, &rec)

	err := NewGithubClient("tok", srv.URL).DeleteBranch(context.Background(), "o", "r", "main")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || apiErr.Message != "Protected branch" {
		t.Fatalf("err = %v", err)
	}
}

func TestGetPullRequest_HeadAndBaseRepo(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"number":90,"state":"open",
	  "head":{"ref":"ai/41","sha":"abc","repo":{"full_name":"o/r"}},
	  "base":{"ref":"main","repo":{"full_name":"o/r"}}}`, &uri)

	pr, err := NewGithubClient("tok", srv.URL).GetPullRequest(context.Background(), "o", "r", 90)
	if err != nil {
		t.Fatal(err)
	}
	if pr.HeadRepo != "o/r" || pr.BaseRepo != "o/r" {
		t.Errorf("head/base repo = %q/%q", pr.HeadRepo, pr.BaseRepo)
	}
}

func TestListPRComments_ReadsIssueCommentThread(t *testing.T) {
	var uri string
	srv := serveJSON(t, `[{"id":1,"body":"b","user":{"login":"github-actions[bot]"},
	  "created_at":"2026-11-25T10:00:00Z"}]`, &uri)

	comments, err := NewGithubClient("tok", srv.URL).ListPRComments(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Author != "github-actions[bot]" || comments[0].Body != "b" {
		t.Fatalf("comments = %+v", comments)
	}
	if uri != "/repos/o/r/issues/7/comments?per_page=100&page=1" {
		t.Errorf("uri = %s", uri)
	}
}
