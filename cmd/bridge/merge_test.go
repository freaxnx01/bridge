package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

const mergeHead = "96fae5fd5d9aa4022a3b5a57a69b50510a563891"
const mergeOld = "1111111111111111111111111111111111111111"

var mergeAt = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

// fakeMerger answers the merge path's reads from its fields and records the
// writes it was asked for.
type fakeMerger struct {
	pr       forge.PullRequest
	comments []forge.Comment
	runs     []forge.CheckRun
	statuses []forge.CommitStatus
	files    []forge.PRFile

	prErr    error
	filesErr error
	mergeErr error
	delErr   error

	calls []string
}

func (f *fakeMerger) GetPullRequest(_ context.Context, owner, repo string, number int) (forge.PullRequest, error) {
	f.calls = append(f.calls, "get "+ref(owner, repo, number))
	return f.pr, f.prErr
}

func (f *fakeMerger) ListPRComments(_ context.Context, _, _ string, _ int) ([]forge.Comment, error) {
	return f.comments, nil
}

func (f *fakeMerger) ListCheckRuns(_ context.Context, _, _, sha string) ([]forge.CheckRun, error) {
	f.calls = append(f.calls, "checks "+sha)
	return f.runs, nil
}

func (f *fakeMerger) ListCommitStatuses(_ context.Context, _, _, _ string) ([]forge.CommitStatus, error) {
	return f.statuses, nil
}

func (f *fakeMerger) ListPullRequestFiles(_ context.Context, owner, repo string, number int) ([]forge.PRFile, error) {
	f.calls = append(f.calls, "files "+ref(owner, repo, number))
	return f.files, f.filesErr
}

func (f *fakeMerger) MergePullRequest(_ context.Context, owner, repo string, number int, sha, method string) error {
	f.calls = append(f.calls, "merge "+ref(owner, repo, number)+" "+sha+" "+method)
	return f.mergeErr
}

func (f *fakeMerger) DeleteBranch(_ context.Context, owner, repo, branch string) error {
	f.calls = append(f.calls, "delete "+owner+"/"+repo+" "+branch)
	return f.delErr
}

func ref(owner, repo string, number int) string {
	return owner + "/" + repo + "#" + strconv.Itoa(number)
}

func (f *fakeMerger) did(call string) bool {
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (f *fakeMerger) didAny(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// greenMerger is a PR nothing blocks: open, not a draft, green checks, an
// approving review on its current head, head branch in the base repo.
func greenMerger() *fakeMerger {
	return &fakeMerger{
		pr: forge.PullRequest{
			Number: 9, State: "open", HeadSHA: mergeHead, HeadRef: "feat/5-x", BaseRef: "main",
			HeadRepo: "o/a", BaseRepo: "o/a", MergeableState: "clean", ChangedFiles: 1,
			Body: "## Summary\n\nCaps it.\n\nCloses #5",
		},
		comments: []forge.Comment{{
			Author: "github-actions[bot]", Created: mergeAt,
			Body: "<!-- review-pr:" + mergeHead + " -->\n## Automated review — verdict: `approve`",
		}},
		runs:  []forge.CheckRun{{Name: "build", Status: "completed", Conclusion: "success"}},
		files: []forge.PRFile{{Path: "a.go", Additions: 3, Deletions: 1}},
	}
}

var mergeReq = api.MergeRequest{Repo: "o/a", Number: 5, PR: 9, HeadSHA: mergeHead, BaseRef: "main"}

var mergeBots = []string{"github-actions[bot]", "my-app[bot]"}

func TestMerge_AllGreen_SquashesWithSHAAndDeletesBranch(t *testing.T) {
	m := greenMerger()

	res, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err != nil {
		t.Fatal(err)
	}
	if want := "merge o/a#9 " + mergeHead + " squash"; !m.did(want) {
		t.Errorf("calls = %q, want %q", m.calls, want)
	}
	if !m.did("delete o/a feat/5-x") {
		t.Errorf("calls = %q, want the head branch deleted", m.calls)
	}
	if res != (api.MergeResult{Merged: true, BranchDeleted: true}) {
		t.Errorf("result = %+v", res)
	}
}

// The request's SHA is only compared; GitHub gets the SHA it reported itself,
// so a request in another case cannot be refused as "head branch was modified".
func TestMerge_UppercaseRequestSHA_MergesWithTheForgesSHA(t *testing.T) {
	m := greenMerger()
	req := mergeReq
	req.HeadSHA = strings.ToUpper(mergeHead)

	if _, err := mergePR(context.Background(), m, mergeBots, req); err != nil {
		t.Fatal(err)
	}
	if want := "merge o/a#9 " + mergeHead + " squash"; !m.did(want) {
		t.Errorf("calls = %q, want %q", m.calls, want)
	}
}

// The dialog names the base it merges into; a PR retargeted since keeps its
// head SHA, so the base is checked on its own.
func TestMerge_BaseChanged_409NoMerge(t *testing.T) {
	m := greenMerger()
	m.pr.BaseRef = "release"

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "base changed" {
		t.Fatalf("err = %v, want base changed", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q, a retargeted PR must not be merged", m.calls)
	}
}

func TestMerge_HeadMoved_409NoMerge(t *testing.T) {
	m := greenMerger()
	m.pr.HeadSHA = "2222222222222222222222222222222222222222"

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "head moved" {
		t.Fatalf("err = %v, want head moved", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q, a moved head must not be merged", m.calls)
	}
}

func TestMerge_NotOpen_NotMergeable(t *testing.T) {
	m := greenMerger()
	m.pr.State = "closed"

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "PR is not open" {
		t.Fatalf("err = %v, want PR is not open", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q", m.calls)
	}
}

func TestMerge_ReviewOnOlderCommit_NotMergeable(t *testing.T) {
	m := greenMerger()
	m.comments = []forge.Comment{{
		Author: "github-actions[bot]", Created: mergeAt,
		Body: "<!-- review-pr:" + mergeOld + " -->\n## Automated review — verdict: `approve`",
	}}

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "review on older commit" {
		t.Fatalf("err = %v, want review on older commit", err)
	}
	if m.didAny("merge ") {
		t.Errorf("calls = %q", m.calls)
	}
}

func TestMerge_ChecksAreReadOnTheHeadSHA(t *testing.T) {
	m := greenMerger()

	if _, err := mergePR(context.Background(), m, mergeBots, mergeReq); err != nil {
		t.Fatal(err)
	}
	if !m.did("checks " + mergeHead) {
		t.Errorf("calls = %q, want the checks read on the head SHA", m.calls)
	}
}

func TestMerge_ForgeRefuses405_ForgeRefused(t *testing.T) {
	m := greenMerger()
	m.mergeErr = &forge.APIError{Status: http.StatusMethodNotAllowed,
		Message: `Required status check "build" is expected.`}

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	var refused api.ErrForgeRefused
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want ErrForgeRefused", err)
	}
	if refused.Status != http.StatusMethodNotAllowed || refused.Message != `Required status check "build" is expected.` {
		t.Errorf("refused = %+v", refused)
	}
	if m.didAny("delete ") {
		t.Errorf("calls = %q, a refused merge must not delete the branch", m.calls)
	}
}

func TestMerge_DeleteFails_Still200(t *testing.T) {
	m := greenMerger()
	m.delErr = errors.New("protected branch")

	res, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err != nil {
		t.Fatalf("err = %v, a failed branch delete must not fail the merge", err)
	}
	if !res.Merged || res.BranchDeleted || res.BranchError == "" {
		t.Errorf("result = %+v", res)
	}
	// The forge's own text goes to the log, never to the client.
	if strings.Contains(res.BranchError, "protected branch") {
		t.Errorf("BranchError = %q, leaks the forge's error text", res.BranchError)
	}
}

// The branch delete needs positive evidence that the head lives in the base
// repo. A fork's branch is not ours, and a head repo GitHub no longer reports
// (head.repo is null once the fork is gone) is not evidence that it ever was —
// read as "ours" it would delete whatever base branch shares the head ref name.
func TestMerge_HeadNotProvablyInBaseRepo_NoDelete(t *testing.T) {
	cases := []struct {
		name     string
		headRepo string
		baseRepo string
		headRef  string
	}{
		{"head in a fork", "contributor/a", "o/a", "feat/5-x"},
		{"fork deleted, head repo null", "", "o/a", "feat/5-x"},
		{"fork deleted, head ref names a base branch", "", "o/a", "main"},
		{"neither repo reported", "", "", "feat/5-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := greenMerger()
			m.pr.HeadRepo, m.pr.BaseRepo, m.pr.HeadRef = tc.headRepo, tc.baseRepo, tc.headRef

			res, err := mergePR(context.Background(), m, mergeBots, mergeReq)
			if err != nil {
				t.Fatal(err)
			}
			if m.didAny("delete ") {
				t.Errorf("calls = %q, the head branch is not ours to delete", m.calls)
			}
			if !res.Merged || res.BranchDeleted || res.BranchError == "" {
				t.Errorf("result = %+v", res)
			}
		})
	}
}

func TestMerge_PRReadFails_Error(t *testing.T) {
	m := greenMerger()
	m.prErr = errors.New("boom")

	_, err := mergePR(context.Background(), m, mergeBots, mergeReq)
	if err == nil {
		t.Fatal("a failed PR read must be an error")
	}
	var notMergeable api.ErrNotMergeable
	var refused api.ErrForgeRefused
	if errors.As(err, &notMergeable) || errors.As(err, &refused) {
		t.Errorf("err = %v, want a plain forge error", err)
	}
}

// cardCollector is the one thing mergeInboxCard reads of the collector: the
// merge card the inbox is currently showing for an issue.
type cardCollector struct {
	card  cockpit.InboxCard
	ok    bool
	asked []string
}

func (c *cardCollector) MergeCard(owner, name string, number int) (cockpit.InboxCard, bool) {
	c.asked = append(c.asked, ref(owner, name, number))
	return c.card, c.ok
}

// shownCard is an inbox showing a merge card for issue 5 and the PR given.
func shownCard(pr int) *cardCollector {
	return &cardCollector{ok: true, card: cockpit.InboxCard{
		Kind: cockpit.CardMerge, Repo: "o/a", Number: 5, Reason: "ready to merge",
		Merge: &cockpit.MergeEvidence{PR: pr, HeadSHA: mergeHead, BaseRef: "main", CanMerge: true},
	}}
}

// mergeClient is a fakeMerger that also satisfies forge.Client, the way a
// resolver hands out a GithubClient.
type mergeClient struct {
	baseClient
	*fakeMerger
}

func mergeRepos() ([]core.Repo, error) {
	return []core.Repo{{Forge: "github", Owner: "o", Name: "a", Path: "/r/a"}}, nil
}

func TestMergeInboxCard_ShownCard_MergesWithTheRepoOwnersClient(t *testing.T) {
	m := greenMerger()
	cards := shownCard(9)
	var gotForge, gotOwner string
	resolve := func(f, o string) forge.Client {
		gotForge, gotOwner = f, o
		return mergeClient{fakeMerger: m}
	}

	res, err := mergeInboxCard(context.Background(), cards, mergeRepos, resolve, mergeBots, mergeReq)
	if err != nil {
		t.Fatal(err)
	}
	if gotForge != "github" || gotOwner != "o" {
		t.Errorf("resolved %s/%s, want the repo owner's client", gotForge, gotOwner)
	}
	if strings.Join(cards.asked, "|") != "o/a#5" {
		t.Errorf("asked = %q, want the card of the requested issue", cards.asked)
	}
	if want := "merge o/a#9 " + mergeHead + " squash"; !m.did(want) {
		t.Errorf("calls = %q, want %q", m.calls, want)
	}
	if !res.Merged {
		t.Errorf("result = %+v", res)
	}
}

// The guard is what keeps an authenticated caller from asking bridge to merge
// an arbitrary PR of a known repo: only the PR the inbox is showing right now.
// The base the server checks is the one the card showed, never one the client
// sends.
func TestMergeInboxCard_ChecksTheShownBase(t *testing.T) {
	cards := shownCard(9)
	cards.card.Merge.BaseRef = "release"
	m := greenMerger()
	resolve := func(string, string) forge.Client { return mergeClient{fakeMerger: m} }

	_, err := mergeInboxCard(context.Background(), cards, mergeRepos, resolve, mergeBots, mergeReq)
	var notMergeable api.ErrNotMergeable
	if !errors.As(err, &notMergeable) || notMergeable.Reason != "base changed" {
		t.Fatalf("err = %v, want base changed", err)
	}
}

func TestMergeInboxCard_NotTheShownPR_RefusedBeforeAnyForgeCall(t *testing.T) {
	humanCard := shownCard(9)
	humanCard.card.Kind = "human"
	noEvidence := shownCard(9)
	noEvidence.card.Merge = nil
	cases := []struct {
		name  string
		cards *cardCollector
	}{
		{"the issue has no card at all", &cardCollector{}},
		{"the card is not a merge card", humanCard},
		{"the card carries no PR evidence", noEvidence},
		{"the card shows another PR", shownCard(10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := greenMerger()
			resolved := false
			resolve := func(string, string) forge.Client {
				resolved = true
				return mergeClient{fakeMerger: m}
			}

			_, err := mergeInboxCard(context.Background(), tc.cards, mergeRepos, resolve, mergeBots, mergeReq)
			var notMergeable api.ErrNotMergeable
			if !errors.As(err, &notMergeable) || notMergeable.Reason != "not on the inbox" {
				t.Fatalf("err = %v, want not on the inbox", err)
			}
			if resolved || len(m.calls) > 0 {
				t.Errorf("resolved = %v, calls = %q, want the request refused before the forge",
					resolved, m.calls)
			}
		})
	}
}

func TestMergeInboxCard_RepoAndClientFailures(t *testing.T) {
	cases := []struct {
		name   string
		client forge.Client
		repos  func() ([]core.Repo, error)
		want   error
	}{
		{"repo not cloned", mergeClient{fakeMerger: greenMerger()},
			func() ([]core.Repo, error) { return nil, nil }, api.ErrUnknownRepo},
		{"repos listing fails", mergeClient{fakeMerger: greenMerger()},
			func() ([]core.Repo, error) { return nil, errors.New("boom") }, nil},
		{"no client for the owner", nil, mergeRepos, api.ErrNoToken},
		{"client cannot merge", baseClient{}, mergeRepos, api.ErrMergeUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(string, string) forge.Client { return tc.client }
			_, err := mergeInboxCard(context.Background(), shownCard(9), tc.repos, resolve, mergeBots, mergeReq)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

var mergeRepo1 = core.Repo{Forge: "github", Owner: "o", Name: "a", Path: "/r/a"}

// The listing carries no mergeable_state, changed_files or head SHA worth
// deciding on, so the evidence must come from a live single-PR read.
func TestLiveEvidence_ReReadsThePRAndDecidesOnIt(t *testing.T) {
	m := greenMerger()
	resolve := func(string, string) forge.Client { return mergeClient{fakeMerger: m} }

	ev, err := liveEvidence(context.Background(), resolve, mergeBots, mergeRepo1,
		forge.PullRequest{Number: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !m.did("get o/a#9") || !m.did("checks "+mergeHead) {
		t.Errorf("calls = %q, want the PR re-read and its checks read on the live head", m.calls)
	}
	if ev.PR != 9 || ev.HeadSHA != mergeHead || !ev.CanMerge {
		t.Errorf("evidence = %+v", ev)
	}
	if m.didAny("merge ") || m.didAny("delete ") {
		t.Errorf("calls = %q, reading evidence must write nothing", m.calls)
	}
}

func TestLiveEvidence_Failures(t *testing.T) {
	failing := greenMerger()
	failing.prErr = errors.New("boom")
	cases := []struct {
		name   string
		client forge.Client
		want   error
	}{
		{"no client for the owner", nil, api.ErrNoToken},
		{"client cannot merge", baseClient{}, api.ErrMergeUnsupported},
		{"the PR read fails", mergeClient{fakeMerger: failing}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(string, string) forge.Client { return tc.client }
			_, err := liveEvidence(context.Background(), resolve, mergeBots, mergeRepo1,
				forge.PullRequest{Number: 9})
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPipelineBots_Table(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want []string
	}{
		{"unset falls back to the Actions bot", "", []string{"github-actions[bot]"}},
		{"blanks and empties are dropped", " a[bot] , ,b[bot] ", []string{"a[bot]", "b[bot]"}},
		{"only separators falls back", " , ", []string{"github-actions[bot]"}},
		{"one entry", "my-app[bot]", []string{"my-app[bot]"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pipelineBots(tc.env)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
