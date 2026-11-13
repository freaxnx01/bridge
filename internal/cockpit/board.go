// Package cockpit builds the WebUI cockpit's board from forge data and local sessions.
package cockpit

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// RunKind says what a Running card is.
type RunKind string

// Running card kinds.
const (
	KindPipeline   RunKind = "pipeline"
	KindEnrichment RunKind = "enrichment"
	KindSession    RunKind = "session"
)

// Labels the cockpit adds to dispatch's set.
const (
	LabelEnrichmentOngoing = "enrichment-ongoing"
	LabelRoadmap           = "roadmap"
)

// RunningCard is one unit of work in flight.
type RunningCard struct {
	Kind      RunKind   `json:"kind"`
	Forge     string    `json:"forge,omitempty"` // "" when the card belongs to no repo
	Repo      string    `json:"repo"`            // "owner/name"; "" when unknown
	Issue     int       `json:"issue,omitempty"`
	Title     string    `json:"title"`
	URL       string    `json:"url,omitempty"`
	Status    string    `json:"status"`
	Since     time.Time `json:"since"`
	SessionID string    `json:"sessionId,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
}

// IssueCard is one issue in the Ready, Held or Needs-enrichment column.
type IssueCard struct {
	Forge  string   `json:"forge"`
	Repo   string   `json:"repo"` // "owner/name"
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Labels []string `json:"labels"`
	Reason string   `json:"reason,omitempty"`
	// Verdict is the live dispatch preview's answer for this card: "next" when
	// the next acting tick would dispatch it, "skip" when a gate stops it, and
	// empty when no preview covered it. VerdictReason carries the skip's reason,
	// Lane the autonomy lane it resolved to, and LaneReason a downgrade.
	Verdict       string `json:"verdict,omitempty"`
	VerdictReason string `json:"verdictReason,omitempty"`
	Lane          string `json:"lane,omitempty"`
	LaneReason    string `json:"laneReason,omitempty"`
}

// SourceError records a data source that failed to refresh.
type SourceError struct {
	Source string    `json:"source"` // "issues owner/name", "runs owner/name", "sessions"
	Error  string    `json:"error"`
	Since  time.Time `json:"since"`
}

// SkippedForge counts the repos on one forge the board has no client for.
type SkippedForge struct {
	Forge string `json:"forge"`
	Repos int    `json:"repos"`
}

// Board is the Pipeline board's whole payload.
type Board struct {
	Running          []RunningCard        `json:"running"`
	Ready            []IssueCard          `json:"ready"`
	Held             []IssueCard          `json:"held"`
	NeedsEnrichment  []IssueCard          `json:"needsEnrichment"`
	DispatchPaused   bool                 `json:"dispatchPaused"`
	CollectedAt      map[string]time.Time `json:"collectedAt"`      // keys: "issues","runs","sessions"
	IntervalsSeconds map[string]int       `json:"intervalsSeconds"` // same keys
	Errors           []SourceError        `json:"errors"`
	Skipped          []SkippedForge       `json:"skipped"` // forges with no client, one entry each
	// Gates are the dispatcher's bounds as of the live preview, nil when there
	// is none (no preview source, or it has not succeeded once yet).
	Gates *dispatch.Gates `json:"gates,omitempty"`
}

// RepoData is the collected state of one repo. Comments holds the run-report
// thread of each inbox candidate whose comments have been read at least once,
// keyed by issue number; CommentErrs holds the last fetch error per issue.
type RepoData struct {
	Forge, Owner, Name, Path string
	Wired                    bool
	Issues                   []forge.Issue
	PRs                      []forge.PullRequest
	Milestones               []forge.Milestone
	Runs                     []forge.WorkflowRun
	Comments                 map[int][]forge.Comment
	CommentErrs              map[int]string
}

// Input is everything BuildBoard needs.
type Input struct {
	Repos          []RepoData
	Sessions       []agentview.Session
	DispatchPaused bool
	// Preview is the dispatcher's read-only verdict on the next acting tick,
	// nil when none is available.
	Preview *dispatch.Tick
}

// dated is a card plus the issue creation time it is sorted by.
type dated struct {
	card    IssueCard
	created time.Time
}

// columns accumulates the classified cards before sorting.
type columns struct {
	running []RunningCard
	held    []dated
	needs   []dated
}

// BuildBoard classifies the input into columns. It fills everything except
// CollectedAt, IntervalsSeconds, Errors and Skipped (both empty, non-nil slices).
func BuildBoard(in Input) Board {
	b := Board{
		Running:         []RunningCard{},
		Ready:           []IssueCard{},
		Held:            []IssueCard{},
		NeedsEnrichment: []IssueCard{},
		Errors:          []SourceError{},
		Skipped:         []SkippedForge{},
		DispatchPaused:  in.DispatchPaused,
	}
	var ready []dispatch.Candidate
	var cols columns
	for _, rd := range in.Repos {
		ready = append(ready, classifyIssues(&cols, rd)...)
		cols.running = append(cols.running, pipelineRuns(rd)...)
	}
	b.Ready = readyCards(ready, in.Preview)
	if in.Preview != nil {
		gates := in.Preview.Gates
		b.Gates = &gates
	}
	b.Running = append(b.Running, cols.running...)
	b.Running = append(b.Running, sessionCards(in)...)
	slices.SortStableFunc(b.Running, func(x, y RunningCard) int { return x.Since.Compare(y.Since) })
	b.Held = sortedCards(cols.held)
	b.NeedsEnrichment = sortedCards(cols.needs)
	return b
}

// readyCards builds the Ready column. With a preview it is the dispatcher's
// own order, each card carrying the verdict the next acting tick would reach;
// without one it is the board's ladder order and no verdicts. A Ready issue
// the preview never saw — collected after it ran — keeps its place at the end
// rather than disappearing from the column.
func readyCards(ready []dispatch.Candidate, preview *dispatch.Tick) []IssueCard {
	ordered := dispatch.Order(ready, nil)
	if preview == nil {
		out := make([]IssueCard, 0, len(ordered))
		for _, c := range ordered {
			out = append(out, card(c.Issue, c.Owner+"/"+c.Repo, ""))
		}
		return out
	}

	byKey := make(map[cardKey]dispatch.Candidate, len(ordered))
	for _, c := range ordered {
		byKey[cardKey{c.Owner + "/" + c.Repo, c.Issue.Number}] = c
	}
	out := make([]IssueCard, 0, len(ordered))
	seen := make(map[cardKey]bool, len(ordered))
	for _, d := range preview.Decisions {
		k := cardKey{d.Candidate.Owner + "/" + d.Candidate.Repo, d.Candidate.Issue.Number}
		c, ok := byKey[k]
		if !ok || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, judged(card(c.Issue, k.repo, ""), d))
	}
	for _, c := range ordered {
		if k := (cardKey{c.Owner + "/" + c.Repo, c.Issue.Number}); !seen[k] {
			out = append(out, card(c.Issue, k.repo, ""))
		}
	}
	return out
}

// cardKey identifies one issue across repos.
type cardKey struct {
	repo   string // "owner/name"
	number int
}

// judged stamps a preview decision onto a card.
func judged(c IssueCard, d dispatch.Decision) IssueCard {
	c.Lane, c.LaneReason = d.Candidate.Lane.Name, d.Candidate.LaneReason
	if d.Dispatch {
		c.Verdict = "next"
		return c
	}
	c.Verdict, c.VerdictReason = "skip", d.Reason
	return c
}

func repoName(rd RepoData) string { return rd.Owner + "/" + rd.Name }

func card(i forge.Issue, repo, reason string) IssueCard {
	labels := i.Labels
	if labels == nil {
		labels = []string{}
	}
	return IssueCard{Forge: i.Forge, Repo: repo, Number: i.Number, Title: i.Title, URL: i.URL, Labels: labels, Reason: reason}
}

// classifyIssues applies the per-issue rules (first match wins), appends to
// the columns, and returns the Ready candidates.
func classifyIssues(b *columns, rd RepoData) []dispatch.Candidate {
	var ready []dispatch.Candidate
	active := dispatch.ActiveMilestone(rd.Milestones)
	for _, i := range rd.Issues {
		switch {
		case dispatch.IsParked(i.Labels) || hasAny(i.Labels, LabelRoadmap):
		case hasAny(i.Labels, LabelEnrichmentOngoing):
			b.running = append(b.running, RunningCard{
				Kind: KindEnrichment, Forge: rd.Forge, Repo: repoName(rd), Issue: i.Number, Title: i.Title,
				URL: i.URL, Status: "enriching", Since: i.Updated,
			})
		case hasAny(i.Labels, dispatch.LabelNeedsEnrichment, dispatch.LabelToBeDefined):
			b.needs = append(b.needs, dated{card(i, repoName(rd), ""), i.Created})
		case hasAny(i.Labels, dispatch.LabelAIImplement):
		case rd.Forge == "github" && rd.Wired:
			ready = append(ready, gate(b, rd, i, active)...)
		}
	}
	return ready
}

// gate asks dispatch.Eligible; eligible issues come back as candidates,
// held ones are appended to b.held, unplanned/empty ones to b.needs, anything else is dropped.
func gate(b *columns, rd RepoData, i forge.Issue, active string) []dispatch.Candidate {
	ok, reason := dispatch.Eligible(i, active, rd.PRs)
	if ok {
		return []dispatch.Candidate{{Issue: i, Owner: rd.Owner, Repo: rd.Name, MilestoneDue: milestoneDue(rd.Milestones, i.Milestone)}}
	}
	switch reason {
	case "outside active milestone", "open PR":
		b.held = append(b.held, dated{card(i, repoName(rd), reason), i.Created})
	case "no implementation plan", "empty body":
		// Not ready, but fixable by enriching: surface it with the reason.
		b.needs = append(b.needs, dated{card(i, repoName(rd), reason), i.Created})
	}
	return nil
}

func milestoneDue(ms []forge.Milestone, title string) time.Time {
	for _, m := range ms {
		if title != "" && m.Title == title {
			return m.DueOn
		}
	}
	return time.Time{}
}

func pipelineRuns(rd RepoData) []RunningCard {
	var out []RunningCard
	linked, single := singleAIImplement(rd.Issues)
	for _, r := range rd.Runs {
		if r.Event != "issues" || (r.Status != "queued" && r.Status != "in_progress") {
			continue
		}
		c := RunningCard{Kind: KindPipeline, Forge: rd.Forge, Repo: repoName(rd), Title: r.Name, URL: r.URL, Status: r.Status, Since: r.Created}
		if single {
			c.Issue, c.Title = linked.Number, linked.Title
		}
		out = append(out, c)
	}
	return out
}

// singleAIImplement returns the repo's only ai-implement issue, if exactly one.
func singleAIImplement(issues []forge.Issue) (forge.Issue, bool) {
	var found []forge.Issue
	for _, i := range issues {
		if hasAny(i.Labels, dispatch.LabelAIImplement) {
			found = append(found, i)
		}
	}
	if len(found) != 1 {
		return forge.Issue{}, false
	}
	return found[0], true
}

func sessionCards(in Input) []RunningCard {
	out := make([]RunningCard, 0, len(in.Sessions))
	for _, s := range in.Sessions {
		repo, forgeName := repoForCWD(in.Repos, s.CWD)
		out = append(out, RunningCard{
			Kind: KindSession, Forge: forgeName, Repo: repo, Title: s.Name,
			Status: s.Status, Since: s.StartedAt, SessionID: s.SessionID, CWD: s.CWD,
		})
	}
	return out
}

// repoForCWD returns the name and forge of the repo whose Path is the longest
// path-boundary prefix of cwd, both empty when no repo contains it.
func repoForCWD(repos []RepoData, cwd string) (repo, forgeName string) {
	bestLen := -1
	c := filepath.Clean(cwd)
	for _, rd := range repos {
		if rd.Path == "" {
			continue
		}
		p := filepath.Clean(rd.Path)
		if (c == p || strings.HasPrefix(c, p+"/")) && len(p) > bestLen {
			repo, forgeName, bestLen = repoName(rd), rd.Forge, len(p)
		}
	}
	return repo, forgeName
}

func sortedCards(ds []dated) []IssueCard {
	slices.SortStableFunc(ds, func(x, y dated) int { return x.created.Compare(y.created) })
	out := make([]IssueCard, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.card)
	}
	return out
}

func hasAny(labels []string, wanted ...string) bool {
	for _, l := range labels {
		if slices.Contains(wanted, l) {
			return true
		}
	}
	return false
}

// isGateCandidate reports whether an issue's labels pass classification rules
// 1-4, i.e. it reaches the dispatch gate (a Ready or Held candidate).
func isGateCandidate(labels []string) bool {
	return !dispatch.IsParked(labels) && !hasAny(labels, LabelRoadmap, LabelEnrichmentOngoing,
		dispatch.LabelNeedsEnrichment, dispatch.LabelToBeDefined, dispatch.LabelAIImplement)
}
