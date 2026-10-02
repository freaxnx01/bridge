package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// fakePRs implements prLister, prReader and runLister on top of a tier-1
// reader. It is deliberately not part of fakeFull, so the existing
// "fully capable client" Capabilities expectation is unaffected.
type fakePRs struct {
	*fakeReader
	prs       []forge.PullRequest
	pr        forge.PullRequest
	files     []forge.PRFile
	checkRuns []forge.CheckRun
	statuses  []forge.CommitStatus
	runs      []forge.WorkflowRun

	prsErr, getErr, filesErr, checksErr, statusesErr, runsErr error

	gotState, gotSHA, gotBranch, gotHeadSHA string
	gotLimit                                int
	deadline                                time.Duration // remaining time on the ctx the last call received; 0 if none
}

func newFakePRs() *fakePRs { return &fakePRs{fakeReader: &fakeReader{name: "github"}} }

func depsFor(c ForgeReader) Deps {
	return Deps{ClientFor: func(string, string) ForgeReader { return c }}
}

func (f *fakePRs) record(ctx context.Context) {
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(dl)
	}
}

func (f *fakePRs) ListPullRequests(ctx context.Context, _, _, state string) ([]forge.PullRequest, error) {
	f.record(ctx)
	f.gotState = state
	return f.prs, f.prsErr
}

func (f *fakePRs) GetPullRequest(ctx context.Context, _, _ string, _ int) (forge.PullRequest, error) {
	f.record(ctx)
	return f.pr, f.getErr
}

func (f *fakePRs) ListPullRequestFiles(ctx context.Context, _, _ string, _ int) ([]forge.PRFile, error) {
	f.record(ctx)
	return f.files, f.filesErr
}

func (f *fakePRs) ListCheckRuns(ctx context.Context, _, _, sha string) ([]forge.CheckRun, error) {
	f.record(ctx)
	f.gotSHA = sha
	return f.checkRuns, f.checksErr
}

func (f *fakePRs) ListCommitStatuses(ctx context.Context, _, _, _ string) ([]forge.CommitStatus, error) {
	f.record(ctx)
	return f.statuses, f.statusesErr
}

func (f *fakePRs) ListWorkflowRuns(ctx context.Context, _, _, branch, headSHA string, limit int) ([]forge.WorkflowRun, error) {
	f.record(ctx)
	f.gotBranch, f.gotHeadSHA, f.gotLimit = branch, headSHA, limit
	return f.runs, f.runsErr
}

func TestHandleListPRs_DefaultsToOpenAndOmitsBody(t *testing.T) {
	c := newFakePRs()
	c.prs = []forge.PullRequest{{Number: 90, Title: "t", Body: "Closes #41", Author: "bot", HeadSHA: "abc"}}

	_, out, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if c.gotState != "open" {
		t.Errorf("state: %q, want open", c.gotState)
	}
	if len(out.PRs) != 1 || out.PRs[0].Number != 90 || out.PRs[0].Author != "bot" {
		t.Fatalf("prs: %+v", out.PRs)
	}
	if out.PRs[0].Body != "" {
		t.Errorf("list output must omit body, got %q", out.PRs[0].Body)
	}
}

func TestHandleListPRs_ClosesFilterUsesDispatchRule(t *testing.T) {
	c := newFakePRs()
	c.prs = []forge.PullRequest{
		{Number: 1, Body: "Closes #41"},
		{Number: 2, Body: "Closes #410"},
		{Number: 3, Body: "fixes #41 and more"},
		{Number: 4, Body: "refs #41"},
	}

	_, out, err := depsFor(c).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "github", Owner: "o", Repo: "r", State: "all", Closes: 41})
	if err != nil {
		t.Fatal(err)
	}
	got := []int{}
	for _, p := range out.PRs {
		got = append(got, p.Number)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("closes=41 kept %v, want [1 3]", got)
	}
	if c.gotState != "all" {
		t.Errorf("state: %q", c.gotState)
	}
}

func TestHandleListPRs_RejectsUnknownState(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "github", Owner: "o", Repo: "r", State: "merged"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleListPRs_RequiresOwnerAndRepo(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleListPRs_ForgeWithoutCapabilityWarns(t *testing.T) {
	_, out, err := depsFor(&fakeReader{name: "forgejo"}).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "forgejo", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if len(out.PRs) != 0 || len(out.Warnings) != 1 || out.Warnings[0] != "forgejo does not support list_prs" {
		t.Fatalf("out: %+v", out)
	}
}

func TestHandleListPRs_UnconfiguredForgeErrors(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return nil }}
	_, _, err := d.handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want not-configured error, got %v", err)
	}
}

func TestHandleListPRs_ClientErrorPropagates(t *testing.T) {
	c := newFakePRs()
	c.prsErr = errors.New("502 bad gateway")
	_, _, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want wrapped client error, got %v", err)
	}
}

func TestHandleListPRs_PassesDeadline(t *testing.T) {
	c := newFakePRs()
	if _, _, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if c.deadline <= 0 || c.deadline > toolTimeout {
		t.Fatalf("forge call ctx deadline remaining %v, want (0, %v]", c.deadline, toolTimeout)
	}
}

func TestHandleGetPR_ReturnsPRFilesAndChecksForHeadSHA(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 90, Body: "Closes #41", HeadSHA: "abc", MergeableState: "blocked", ChangedFiles: 1}
	c.files = []forge.PRFile{{Path: "a.go", Additions: 3, Deletions: 1}}
	c.checkRuns = []forge.CheckRun{{Name: "build", Status: "completed", Conclusion: "failure", App: "github-actions"}}
	c.statuses = []forge.CommitStatus{{Context: "ci/legacy", State: "success"}}

	_, out, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 90})
	if err != nil {
		t.Fatal(err)
	}
	if out.PR == nil || out.PR.Body != "Closes #41" || out.PR.MergeableState != "blocked" {
		t.Fatalf("pr: %+v", out.PR)
	}
	if c.gotSHA != "abc" {
		t.Errorf("checks queried for %q, want head SHA abc", c.gotSHA)
	}
	if len(out.Files) != 1 || out.FilesTruncated {
		t.Errorf("files: %+v truncated=%v", out.Files, out.FilesTruncated)
	}
	if len(out.Checks.CheckRuns) != 1 || out.Checks.CheckRuns[0].Conclusion != "failure" {
		t.Errorf("check runs: %+v", out.Checks.CheckRuns)
	}
	if len(out.Checks.Statuses) != 1 || out.Checks.Statuses[0].State != "success" {
		t.Errorf("statuses: %+v", out.Checks.Statuses)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings: %v", out.Warnings)
	}
}

func TestHandleGetPR_CapsFilesAndFlagsTruncation(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 1, HeadSHA: "abc", ChangedFiles: 250}
	for i := 0; i < 100; i++ {
		c.files = append(c.files, forge.PRFile{Path: fmt.Sprintf("f%d.go", i)})
	}

	_, out, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != maxPRFiles || !out.FilesTruncated {
		t.Fatalf("want %d files and truncated, got %d truncated=%v", maxPRFiles, len(out.Files), out.FilesTruncated)
	}
}

func TestHandleGetPR_ChecksFailureIsPartialResult(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 1, HeadSHA: "abc", ChangedFiles: 1}
	c.files = []forge.PRFile{{Path: "a.go"}}
	c.checksErr = errors.New("403 resource not accessible")

	_, out, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("a failed check-runs fetch must not fail the call: %v", err)
	}
	if out.PR == nil || len(out.Files) != 1 {
		t.Fatalf("pr and files must still be returned: %+v", out)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "check-runs") || !strings.Contains(out.Warnings[0], "403") {
		t.Fatalf("warnings: %v", out.Warnings)
	}
	if out.Checks.CheckRuns == nil {
		t.Error("check_runs must be an empty list, not null")
	}
}

func TestHandleGetPR_PRFetchFailureErrors(t *testing.T) {
	c := newFakePRs()
	c.getErr = errors.New("404 not found")
	_, _, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 9})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want wrapped 404, got %v", err)
	}
}

func TestHandleGetPR_RequiresPositiveNumber(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleGetPR_ForgeWithoutCapabilityWarns(t *testing.T) {
	_, out, err := depsFor(&fakeReader{name: "forgejo"}).handleGetPR(context.Background(), nil,
		getPRInput{Forge: "forgejo", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if out.PR != nil || len(out.Warnings) != 1 || out.Warnings[0] != "forgejo does not support get_pr" {
		t.Fatalf("out: %+v", out)
	}
}

func TestHandleGetPR_PassesDeadline(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 1, HeadSHA: "abc"}
	if _, _, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 1}); err != nil {
		t.Fatal(err)
	}
	if c.deadline <= 0 || c.deadline > toolTimeout {
		t.Fatalf("forge call ctx deadline remaining %v, want (0, %v]", c.deadline, toolTimeout)
	}
}
