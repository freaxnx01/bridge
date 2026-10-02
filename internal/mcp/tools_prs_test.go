package mcp

import (
	"context"
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
