package cockpit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// mergeHead is the PR head SHA every fixture below is pinned to, in the full
// 40-hex form the pipeline's review comments carry.
const mergeHead = "96fae5fd5d9aa4022a3b5a57a69b50510a563891"

// mergeOld is an earlier commit on the same PR.
const mergeOld = "1111111111111111111111111111111111111111"

// mergeBots are the two trusted pipeline identities: the Actions bot and a
// pipeline App's bot, which posts the re-reviews when one is configured.
var mergeBots = []string{"github-actions[bot]", "my-app[bot]"}

var mergeT0 = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

// reviewFallback is the review comment shape bridge's own agent.yml posts
// today: an HTML anchor naming the head SHA plus a verdict line.
func reviewFallback(sha, verdict, author string, at time.Time) forge.Comment {
	return forge.Comment{Author: author, Created: at,
		Body: "<!-- review-pr:" + sha + " -->\n## Automated review — verdict: `" + verdict +
			"`\n\n_Agent: claude · head: `" + sha + "`_"}
}

// reviewMarker is the same comment with agent-workflow ADR-022's machine-readable
// marker as its last line.
func reviewMarker(sha, verdict, author string, at time.Time) forge.Comment {
	return forge.Comment{Author: author, Created: at,
		Body: "<!-- review-pr:" + sha + " -->\n## Automated review — verdict: `" + verdict +
			"`\n<!-- ai-review: {\"v\":1,\"verdict\":\"" + verdict + "\",\"head_sha\":\"" + sha +
			"\",\"blocking\":0,\"nits\":2} -->"}
}

func TestReviewVerdict_Table(t *testing.T) {
	tests := []struct {
		name         string
		comments     []forge.Comment
		wantVerdict  string
		wantCurrent  bool
		wantSource   string
		wantBlocking *int
		wantNits     *int
	}{
		{"marker approve on head",
			[]forge.Comment{reviewMarker(mergeHead, "approve", "github-actions[bot]", mergeT0)},
			"approve", true, "marker", intp(0), intp(2)},
		{"fallback approve on head",
			[]forge.Comment{reviewFallback(mergeHead, "approve", "github-actions[bot]", mergeT0)},
			"approve", true, "fallback", nil, nil},
		{"approve on an older commit only",
			[]forge.Comment{reviewFallback(mergeOld, "approve", "github-actions[bot]", mergeT0)},
			"approve", false, "fallback", nil, nil},
		{"older approve, newer request_changes on head",
			[]forge.Comment{
				reviewFallback(mergeOld, "approve", "github-actions[bot]", mergeT0),
				reviewFallback(mergeHead, "request_changes", "github-actions[bot]", mergeT0.Add(time.Hour)),
			},
			"request_changes", true, "fallback", nil, nil},
		{"re-review by the pipeline App's bot counts",
			[]forge.Comment{
				reviewFallback(mergeOld, "request_changes", "github-actions[bot]", mergeT0),
				reviewMarker(mergeHead, "approve", "my-app[bot]", mergeT0.Add(time.Hour)),
			},
			"approve", true, "marker", intp(0), intp(2)},
		{"uppercase SHA in the comment is still the head",
			[]forge.Comment{reviewFallback("96FAE5FD5D9AA4022A3B5A57A69B50510A563891", "approve",
				"github-actions[bot]", mergeT0)},
			"approve", true, "fallback", nil, nil},
		{"a trusted bot login cased differently still counts",
			[]forge.Comment{reviewFallback(mergeHead, "approve", "GitHub-Actions[bot]", mergeT0)},
			"approve", true, "fallback", nil, nil},
		{"an abbreviated anchor on the head is current",
			[]forge.Comment{reviewFallback(mergeHead[:7], "approve", "github-actions[bot]", mergeT0)},
			"approve", true, "fallback", nil, nil},
		{"an abbreviated anchor on another commit is not current",
			[]forge.Comment{reviewFallback(mergeOld[:12], "approve", "github-actions[bot]", mergeT0)},
			"approve", false, "fallback", nil, nil},
		{"a marker SHA too short to be a commit reference is not the head",
			[]forge.Comment{reviewMarker(mergeHead[:6], "approve", "github-actions[bot]", mergeT0)},
			"approve", false, "marker", intp(0), intp(2)},
		{"the latest on the head wins over a newer one on another commit",
			[]forge.Comment{
				reviewFallback(mergeHead, "approve", "github-actions[bot]", mergeT0),
				reviewFallback(mergeOld, "block", "github-actions[bot]", mergeT0.Add(time.Hour)),
			},
			"approve", true, "fallback", nil, nil},
		{"no review comments at all", nil, "", false, "", nil, nil},
		{"a comment that is not a review", []forge.Comment{
			{Author: "github-actions[bot]", Body: "## ai-implement run\n\n- outcome: ok", Created: mergeT0}},
			"", false, "", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LatestVerdict(tc.comments, mergeHead, mergeBots)
			if tc.wantVerdict == "" {
				if got != nil {
					t.Fatalf("verdict = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("verdict = nil, want one")
			}
			if got.Verdict != tc.wantVerdict || got.Current != tc.wantCurrent || got.Source != tc.wantSource {
				t.Errorf("got %+v, want verdict %q current %v source %q",
					got, tc.wantVerdict, tc.wantCurrent, tc.wantSource)
			}
			if !samePtr(got.Blocking, tc.wantBlocking) || !samePtr(got.Nits, tc.wantNits) {
				t.Errorf("blocking/nits = %v/%v", deref(got.Blocking), deref(got.Nits))
			}
		})
	}
}

func TestReviewVerdict_UntrustedAuthor_Ignored(t *testing.T) {
	comments := []forge.Comment{reviewMarker(mergeHead, "approve", "mallory", mergeT0)}
	if got := LatestVerdict(comments, mergeHead, mergeBots); got != nil {
		t.Fatalf("verdict = %+v, want nil for an untrusted author", got)
	}
}

func TestReviewVerdict_MalformedMarker_FallsBack(t *testing.T) {
	tests := []struct {
		name string
		last string
	}{
		{"marker JSON contains the comment terminator", `<!-- ai-review: {"v":1,"verdict": --> -->`},
		{"marker JSON is unparsable", `<!-- ai-review: {"v":1,"verdict":} -->`},
		{"marker is a later schema version", `<!-- ai-review: {"v":2,"verdict":"block"} -->`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := reviewFallback(mergeHead, "approve", "github-actions[bot]", mergeT0)
			c.Body += "\n" + tc.last
			got := LatestVerdict([]forge.Comment{c}, mergeHead, mergeBots)
			if got == nil {
				t.Fatal("verdict = nil, want the fallback form")
			}
			if got.Verdict != "approve" || got.Source != "fallback" || !got.Current {
				t.Errorf("got %+v, want approve/fallback/current", got)
			}
		})
	}
}

func TestSummarizeChecks_Table(t *testing.T) {
	run := func(status, conclusion string) forge.CheckRun {
		return forge.CheckRun{Name: status + "/" + conclusion, Status: status, Conclusion: conclusion}
	}
	status := func(state string) forge.CommitStatus {
		return forge.CommitStatus{Context: state, State: state}
	}
	tests := []struct {
		name       string
		runs       []forge.CheckRun
		statuses   []forge.CommitStatus
		wantState  string
		wantPassed int
		wantTotal  int
	}{
		{"all green", []forge.CheckRun{run("completed", "success"), run("completed", "neutral"),
			run("completed", "skipped")}, []forge.CommitStatus{status("success")}, "green", 4, 4},
		{"one in progress", []forge.CheckRun{run("completed", "success"), run("in_progress", "")},
			nil, "running", 1, 2},
		{"one queued", []forge.CheckRun{run("queued", "")}, nil, "running", 0, 1},
		{"failing beats running", []forge.CheckRun{run("in_progress", ""), run("completed", "failure")},
			nil, "failing", 0, 2},
		{"cancelled counts as failing", []forge.CheckRun{run("completed", "cancelled")}, nil, "failing", 0, 1},
		{"pending status is running", []forge.CheckRun{run("completed", "success")},
			[]forge.CommitStatus{status("pending")}, "running", 1, 2},
		{"failed status is failing", nil, []forge.CommitStatus{status("failure")}, "failing", 0, 1},
		{"errored status is failing", nil, []forge.CommitStatus{status("error")}, "failing", 0, 1},
		{"nothing reported", nil, nil, "none", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SummarizeChecks(tc.runs, tc.statuses)
			want := ChecksSummary{State: tc.wantState, Passed: tc.wantPassed, Total: tc.wantTotal}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestSummarySection_Table(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"section before the next heading", "## Summary\n\nCaps it.\n\n## Changes\n- x", "Caps it."},
		{"last section in the body", "## Changes\n- x\n\n## Summary\nCaps it.", "Caps it."},
		{"multi-paragraph section", "## Summary\n\nOne.\n\nTwo.\n\n## Testing\n- t", "One.\n\nTwo."},
		{"heading only, no text", "## Summary\n\n## Changes\n- x", ""},
		{"no summary heading", "Closes #5\n\n## Changes\n- x", ""},
		{"empty body", "", ""},
		{"a deeper heading does not end the section", "## Summary\n\nOne.\n\n### Detail\nTwo.\n\n## Testing",
			"One.\n\n### Detail\nTwo."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SummarySection(tc.body); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEligibility_Table(t *testing.T) {
	// green is the all-clear evidence every row below spoils in exactly one way.
	green := func() MergeEvidence {
		return MergeEvidence{
			PR: 9, HeadSHA: mergeHead, MergeableState: "clean",
			Checks: ChecksSummary{State: "green", Passed: 3, Total: 3},
			Review: &ReviewVerdict{Verdict: "approve", HeadSHA: mergeHead, Current: true, Source: "marker"},
		}
	}
	tests := []struct {
		name       string
		spoil      func(*MergeEvidence)
		wantMerge  bool
		wantReason string
	}{
		{"all clear", func(*MergeEvidence) {}, true, ""},
		{"stale beats everything", func(e *MergeEvidence) {
			e.Stale, e.Draft = true, true
		}, false, "forge read failed (evidence stale)"},
		{"draft", func(e *MergeEvidence) { e.Draft = true }, false, "draft"},
		{"conflicts", func(e *MergeEvidence) { e.MergeableState = "dirty" }, false, "conflicts"},
		{"checks running", func(e *MergeEvidence) { e.Checks.State = "running" }, false, "checks running"},
		{"checks failing", func(e *MergeEvidence) { e.Checks.State = "failing" }, false, "checks failing"},
		{"no checks reported", func(e *MergeEvidence) {
			e.Checks = ChecksSummary{State: "none"}
		}, false, "no checks reported"},
		{"no review yet", func(e *MergeEvidence) { e.Review = nil }, false, "no review yet"},
		{"review on an older commit", func(e *MergeEvidence) {
			e.Review.Current, e.Review.HeadSHA = false, mergeOld
		}, false, "review on older commit"},
		{"review requests changes", func(e *MergeEvidence) {
			e.Review.Verdict = "request_changes"
		}, false, "review: request_changes"},
		{"review blocks", func(e *MergeEvidence) { e.Review.Verdict = "block" }, false, "review: block"},
		{"review failed", func(e *MergeEvidence) { e.Review.Verdict = "review_failed" }, false, "review: review_failed"},
		// GitHub decides these three at merge time; bridge does not second-guess
		// branch protection it cannot fully read.
		{"mergeable state unknown", func(e *MergeEvidence) { e.MergeableState = "unknown" }, true, ""},
		{"mergeable state blocked", func(e *MergeEvidence) { e.MergeableState = "blocked" }, true, ""},
		{"mergeable state behind", func(e *MergeEvidence) { e.MergeableState = "behind" }, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := green()
			tc.spoil(&e)
			canMerge, reason := Eligibility(e)
			if canMerge != tc.wantMerge || reason != tc.wantReason {
				t.Errorf("got (%v, %q), want (%v, %q)", canMerge, reason, tc.wantMerge, tc.wantReason)
			}
		})
	}
}

func TestClosingPipelinePRs_FiltersAuthorAndCloses(t *testing.T) {
	prs := []forge.PullRequest{
		{Number: 9, Body: "Closes #5", Author: "github-actions[bot]"},
		{Number: 10, Body: "Closes #5", Author: "alice"},
		{Number: 11, Body: "Closes #6", Author: "github-actions[bot]"},
		{Number: 12, Body: "Refs #5", Author: "my-app[bot]"},
		{Number: 13, Body: "Fixes #5", Author: "my-app[bot]"},
	}
	got := ClosingPipelinePRs(prs, 5, mergeBots)
	if len(got) != 2 || got[0].Number != 9 || got[1].Number != 13 {
		t.Fatalf("got %+v, want PRs 9 and 13", got)
	}
}

// GitHub logins are case-insensitive, so a BRIDGE_PIPELINE_BOTS entry whose
// case differs from the API's user.login must not drop the PR.
func TestClosingPipelinePRs_BotLoginCase_Ignored(t *testing.T) {
	prs := []forge.PullRequest{{Number: 9, Body: "Closes #5", Author: "My-App[Bot]"}}
	if got := ClosingPipelinePRs(prs, 5, mergeBots); len(got) != 1 {
		t.Fatalf("got %+v, want PR 9", got)
	}
}

func TestBuildEvidence_FillsCardFieldsAndVerdict(t *testing.T) {
	pr := forge.PullRequest{
		Number: 9, URL: "https://github.com/o/a/pull/9", HeadSHA: mergeHead, HeadRef: "ai/5",
		BaseRef: "main", MergeableState: "clean", ChangedFiles: 150,
		Body: "## Summary\n\nCaps it.\n\n## Changes\n- x",
	}
	files := make([]forge.PRFile, 100)
	e := BuildEvidence(pr,
		[]forge.Comment{reviewMarker(mergeHead, "approve", "github-actions[bot]", mergeT0)},
		[]forge.CheckRun{{Status: "completed", Conclusion: "success"}}, nil, files, mergeBots)

	if e.PR != 9 || e.PRURL != pr.URL || e.HeadSHA != mergeHead || e.HeadRef != "ai/5" || e.BaseRef != "main" {
		t.Errorf("identity fields: %+v", e)
	}
	if e.Summary != "Caps it." {
		t.Errorf("summary = %q", e.Summary)
	}
	if e.Review == nil || e.Review.Verdict != "approve" || !e.Review.Current {
		t.Errorf("review = %+v", e.Review)
	}
	if e.Checks != (ChecksSummary{State: "green", Passed: 1, Total: 1}) {
		t.Errorf("checks = %+v", e.Checks)
	}
	if !e.FilesTruncated || len(e.Files) != 100 {
		t.Errorf("files = %d, truncated %v", len(e.Files), e.FilesTruncated)
	}
	if !e.CanMerge || e.Reason != "" {
		t.Errorf("canMerge = %v, reason = %q", e.CanMerge, e.Reason)
	}
}

func TestBuildEvidence_NoSummarySection_EmptyAndStillMergeable(t *testing.T) {
	pr := forge.PullRequest{Number: 9, HeadSHA: mergeHead, ChangedFiles: 1, Body: "Closes #5"}
	e := BuildEvidence(pr,
		[]forge.Comment{reviewFallback(mergeHead, "approve", "github-actions[bot]", mergeT0)},
		[]forge.CheckRun{{Status: "completed", Conclusion: "success"}}, nil,
		[]forge.PRFile{{Path: "a.go", Additions: 1}}, mergeBots)

	if e.Summary != "" || e.FilesTruncated {
		t.Errorf("summary = %q, truncated = %v", e.Summary, e.FilesTruncated)
	}
	if !e.CanMerge {
		t.Errorf("canMerge = false, reason %q", e.Reason)
	}
}

func TestBuildEvidence_FilesCarryNoPatch(t *testing.T) {
	pr := forge.PullRequest{Number: 9, HeadSHA: mergeHead, ChangedFiles: 2}
	files := []forge.PRFile{
		{Path: "a.go", Status: "modified", Additions: 1, Patch: "@@ -1 +1 @@\n+x"},
		{Path: "b.go", Status: "added", Additions: 2, Patch: "@@ -0,0 +1 @@\n+y"},
	}
	e := BuildEvidence(pr, nil, nil, nil, files, mergeBots)

	for i, f := range e.Files {
		if f.Patch != "" {
			t.Errorf("files[%d].Patch = %q, want empty", i, f.Patch)
		}
		if f.Status == "" || f.Additions == 0 {
			t.Errorf("files[%d] lost its metadata: %+v", i, f)
		}
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"patch"`) {
		t.Errorf("evidence JSON carries a patch: %s", raw)
	}
	// The caller's own slice must not be reused: the collector hands the same
	// files to the diff path, which needs the patches.
	if files[0].Patch == "" {
		t.Error("BuildEvidence cleared the caller's patch")
	}
}

func intp(n int) *int { return &n }

func samePtr(got, want *int) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
