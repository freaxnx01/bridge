package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if len(files) != 1 || files[0] != (PRFile{Path: "a/b.go", Additions: 5, Deletions: 2}) {
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
