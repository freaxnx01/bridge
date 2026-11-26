package cockpit

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// Check aggregate states of a PR's head commit.
const (
	ChecksGreen   = "green"
	ChecksRunning = "running"
	ChecksFailing = "failing"
	ChecksNone    = "none"
)

// Where a review verdict was read from: ADR-022's machine-readable marker, or
// the older anchor-plus-verdict-line shape bridge's own agent.yml still posts.
const (
	VerdictSourceMarker   = "marker"
	VerdictSourceFallback = "fallback"
)

// ReasonSeveralPRs is the merge card's reason when more than one trusted PR
// closes the issue. Bridge does not pick one: that is an anomaly for a person.
const ReasonSeveralPRs = "several open PRs close this issue"

// ReasonStale is the reason for evidence whose last forge read failed.
const ReasonStale = "forge read failed (evidence stale)"

// verdictApprove is the only verdict that allows a merge.
const verdictApprove = "approve"

// minSHAPrefix is the shortest commit reference a verdict may carry — the
// fallback anchor's own lower bound. Anything shorter is not a SHA and never
// matches a head.
const minSHAPrefix = 7

// passingConclusions are the check-run conclusions that count as passed. A run
// GitHub skipped or marked neutral never ran against the code, so neither
// blocks the merge the way a failure does.
var passingConclusions = []string{"success", "neutral", "skipped"}

var (
	// reviewMarkerRE matches ADR-022's marker line. The JSON must close with
	// "}" before the comment terminator, so a payload containing "-->" cannot
	// be read as a marker at all.
	reviewMarkerRE = regexp.MustCompile(`^<!-- ai-review: (\{.*\}) -->$`)
	// reviewAnchorRE matches the review comment's first-line anchor, which
	// carries the head SHA the review ran against.
	reviewAnchorRE = regexp.MustCompile(`^<!-- review-pr:([0-9a-fA-F]{7,64}) -->$`)
	// verdictLineRE matches the human-readable verdict of the fallback shape.
	verdictLineRE = regexp.MustCompile("verdict: `(approve|request_changes|block|review_failed)`")
	// summaryHeadingRE matches the PR body's "## Summary" heading.
	summaryHeadingRE = regexp.MustCompile(`(?i)^##\s+summary\s*$`)
	// topHeadingRE matches a heading that ends the summary section: another
	// level-1 or level-2 heading, but not a "###" subsection inside it.
	topHeadingRE = regexp.MustCompile(`^#{1,2}\s`)
)

// ReviewVerdict is the AI review's answer for one commit. Current says whether
// HeadSHA is the PR's current head — a verdict on an older commit says nothing
// about what would be merged. Blocking and Nits are nil when the verdict came
// from the fallback shape, which does not carry counts.
type ReviewVerdict struct {
	Verdict  string `json:"verdict"` // approve | request_changes | block | review_failed
	HeadSHA  string `json:"headSha"`
	Current  bool   `json:"current"`
	Blocking *int   `json:"blocking,omitempty"`
	Nits     *int   `json:"nits,omitempty"`
	Source   string `json:"source"`
}

// ChecksSummary is the aggregate of a head commit's check runs and commit
// statuses. Passed counts the ones that are through and green, Total all of
// them; a head with neither is State ChecksNone, not green.
type ChecksSummary struct {
	State  string `json:"state"`
	Passed int    `json:"passed"`
	Total  int    `json:"total"`
}

// MergeEvidence is everything the merge card shows and everything the merge
// decision is made from. Stale marks evidence whose last read failed, so the
// card shows the previous reading and refuses the merge.
type MergeEvidence struct {
	PR             int            `json:"pr"`
	PRURL          string         `json:"prUrl"`
	HeadSHA        string         `json:"headSha"`
	HeadRef        string         `json:"headRef"`
	BaseRef        string         `json:"baseRef"`
	Draft          bool           `json:"draft"`
	MergeableState string         `json:"mergeableState"`
	Summary        string         `json:"summary"`
	Review         *ReviewVerdict `json:"review"`
	Checks         ChecksSummary  `json:"checks"`
	Files          []forge.PRFile `json:"files"`
	FilesTruncated bool           `json:"filesTruncated"`
	Stale          bool           `json:"stale"`
	CanMerge       bool           `json:"canMerge"`
	Reason         string         `json:"reason"` // "" iff CanMerge
}

// LatestVerdict is the review verdict for headSHA, read only from comments
// authored by a trusted pipeline identity. The latest verdict on the current
// head wins; with none on it, the latest verdict on any commit comes back with
// Current false, so the card can say "review on older commit" rather than
// "no review yet". Nil means no trusted comment carried a verdict at all.
func LatestVerdict(comments []forge.Comment, headSHA string, bots []string) *ReviewVerdict {
	var onHead, latest *ReviewVerdict
	var onHeadAt, latestAt time.Time
	for _, c := range comments {
		if !isTrusted(bots, c.Author) {
			continue
		}
		v := parseVerdict(c.Body)
		if v == nil {
			continue
		}
		if latest == nil || !c.Created.Before(latestAt) {
			latest, latestAt = v, c.Created
		}
		if sameCommit(v.HeadSHA, headSHA) && (onHead == nil || !c.Created.Before(onHeadAt)) {
			onHead, onHeadAt = v, c.Created
		}
	}
	if onHead != nil {
		onHead.Current = true
		return onHead
	}
	return latest
}

// isTrusted reports whether login is one of the trusted pipeline identities.
// GitHub logins are case-insensitive, so a BRIDGE_PIPELINE_BOTS entry cased
// differently from the API's user.login still counts — otherwise the card
// silently never appears, which is a hard failure to diagnose.
func isTrusted(bots []string, login string) bool {
	return slices.ContainsFunc(bots, func(b string) bool { return strings.EqualFold(b, login) })
}

// sameCommit reports whether a verdict's SHA names headSHA. The fallback anchor
// may carry an abbreviated SHA, which matches on its prefix: compared whole it
// would never equal the 40-hex head, leaving the card stuck on "review on older
// commit" with no way to tell it from a genuinely stale verdict.
func sameCommit(verdictSHA, headSHA string) bool {
	if len(verdictSHA) < minSHAPrefix || len(verdictSHA) > len(headSHA) {
		return false
	}
	return strings.EqualFold(verdictSHA, headSHA[:len(verdictSHA)])
}

// parseVerdict reads one comment body: ADR-022's marker when its last non-empty
// line carries a v1 payload, else the anchor-plus-verdict-line fallback. Nil
// means the comment is not a review report.
func parseVerdict(body string) *ReviewVerdict {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if v := markerVerdict(lines); v != nil {
		return v
	}
	return fallbackVerdict(lines)
}

// markerVerdict reads the marker off the last non-empty line. A line that is
// not a marker, a payload that is not JSON, and a schema version this code was
// not written against all read as "no marker" so the fallback gets its turn.
func markerVerdict(lines []string) *ReviewVerdict {
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if last = strings.TrimSpace(lines[i]); last != "" {
			break
		}
	}
	m := reviewMarkerRE.FindStringSubmatch(last)
	if m == nil {
		return nil
	}
	var payload struct {
		V        int    `json:"v"`
		Verdict  string `json:"verdict"`
		HeadSHA  string `json:"head_sha"`
		Blocking *int   `json:"blocking"`
		Nits     *int   `json:"nits"`
	}
	if err := json.Unmarshal([]byte(m[1]), &payload); err != nil {
		return nil
	}
	if payload.V != 1 || payload.Verdict == "" || payload.HeadSHA == "" {
		return nil
	}
	return &ReviewVerdict{
		Verdict: payload.Verdict, HeadSHA: payload.HeadSHA,
		Blocking: payload.Blocking, Nits: payload.Nits, Source: VerdictSourceMarker,
	}
}

// fallbackVerdict reads the pre-marker shape: the head SHA from the first
// line's anchor plus the verdict from the report's own verdict line.
func fallbackVerdict(lines []string) *ReviewVerdict {
	if len(lines) == 0 {
		return nil
	}
	anchor := reviewAnchorRE.FindStringSubmatch(strings.TrimSpace(lines[0]))
	if anchor == nil {
		return nil
	}
	for _, l := range lines {
		if m := verdictLineRE.FindStringSubmatch(l); m != nil {
			return &ReviewVerdict{Verdict: m[1], HeadSHA: anchor[1], Source: VerdictSourceFallback}
		}
	}
	return nil
}

// SummarizeChecks aggregates a head commit's check runs and commit statuses.
// A failure outweighs a run still going: the operator needs to know the branch
// is broken now, not that something is still reporting.
func SummarizeChecks(runs []forge.CheckRun, statuses []forge.CommitStatus) ChecksSummary {
	if len(runs)+len(statuses) == 0 {
		return ChecksSummary{State: ChecksNone}
	}
	s := ChecksSummary{Total: len(runs) + len(statuses)}
	running, failing := false, false
	for _, r := range runs {
		switch {
		case r.Status != "completed":
			running = true
		case slices.Contains(passingConclusions, r.Conclusion):
			s.Passed++
		default:
			failing = true
		}
	}
	for _, st := range statuses {
		switch st.State {
		case "success":
			s.Passed++
		case "pending":
			running = true
		default:
			failing = true
		}
	}
	switch {
	case failing:
		s.State = ChecksFailing
	case running:
		s.State = ChecksRunning
	default:
		s.State = ChecksGreen
	}
	return s
}

// SummarySection is the text under the PR body's "## Summary" heading, up to
// the next top-level heading. Empty when the body has no such section — the
// card says so rather than guessing a summary from another paragraph.
func SummarySection(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if summaryHeadingRE.MatchString(strings.TrimSpace(l)) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if topHeadingRE.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

// ClosingPipelinePRs returns the PRs that close the issue and were opened by a
// trusted pipeline identity. A hand-written PR closing the same issue is not a
// merge candidate: the card's whole premise is an agent's own work.
func ClosingPipelinePRs(prs []forge.PullRequest, issue int, bots []string) []forge.PullRequest {
	var out []forge.PullRequest
	for _, p := range prs {
		if isTrusted(bots, p.Author) && dispatch.ClosesIssue(p.Body, issue) {
			out = append(out, p)
		}
	}
	return out
}

// BuildEvidence assembles one merge candidate's evidence and decides on it.
func BuildEvidence(pr forge.PullRequest, comments []forge.Comment, runs []forge.CheckRun,
	statuses []forge.CommitStatus, files []forge.PRFile, bots []string) MergeEvidence {
	e := MergeEvidence{
		PR: pr.Number, PRURL: pr.URL, HeadSHA: pr.HeadSHA, HeadRef: pr.HeadRef, BaseRef: pr.BaseRef,
		Draft: pr.Draft, MergeableState: pr.MergeableState,
		Summary: SummarySection(pr.Body),
		Review:  LatestVerdict(comments, pr.HeadSHA, bots),
		Checks:  SummarizeChecks(runs, statuses),
		Files:   withoutPatches(files), FilesTruncated: pr.ChangedFiles > len(files),
	}
	e.CanMerge, e.Reason = Eligibility(e)
	return e
}

// withoutPatches copies the file list with every patch dropped. The evidence
// travels in the open GET /api/inbox, which must carry no source code: the
// patch is served only by the token-guarded diff endpoint.
func withoutPatches(files []forge.PRFile) []forge.PRFile {
	if files == nil {
		return nil
	}
	out := make([]forge.PRFile, len(files))
	for i, f := range files {
		f.Patch = ""
		out[i] = f
	}
	return out
}

// Eligibility is the single rule the card and the merge endpoint both apply:
// may this PR be merged, and if not, why. First blocker wins.
//
// Only "dirty" is judged here. GitHub computes mergeable_state lazily and
// refuses the merge itself when branch protection is unsatisfied, so
// "unknown", "blocked" and "behind" are left to it rather than guessed at.
func Eligibility(e MergeEvidence) (canMerge bool, reason string) {
	switch {
	case e.Stale:
		return false, ReasonStale
	case e.Draft:
		return false, "draft"
	case e.MergeableState == "dirty":
		return false, "conflicts"
	case e.Checks.State == ChecksRunning:
		return false, "checks running"
	case e.Checks.State == ChecksFailing:
		return false, "checks failing"
	case e.Checks.State == ChecksNone:
		return false, "no checks reported"
	case e.Review == nil:
		return false, "no review yet"
	case !e.Review.Current:
		return false, "review on older commit"
	case e.Review.Verdict != verdictApprove:
		return false, "review: " + e.Review.Verdict
	}
	return true, ""
}
