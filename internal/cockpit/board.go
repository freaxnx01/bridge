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
	Repo      string    `json:"repo"` // "owner/name"; "" when unknown
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
}

// SourceError records a data source that failed to refresh.
type SourceError struct {
	Source string    `json:"source"` // "issues owner/name", "runs owner/name", "sessions"
	Error  string    `json:"error"`
	Since  time.Time `json:"since"`
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
}

// RepoData is the collected state of one repo.
type RepoData struct {
	Forge, Owner, Name, Path string
	Wired                    bool
	Issues                   []forge.Issue
	PRs                      []forge.PullRequest
	Milestones               []forge.Milestone
	Runs                     []forge.WorkflowRun
}

// Input is everything BuildBoard needs.
type Input struct {
	Repos          []RepoData
	Sessions       []agentview.Session
	DispatchPaused bool
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
// CollectedAt, IntervalsSeconds and Errors (Errors is an empty, non-nil slice).
func BuildBoard(in Input) Board {
	b := Board{
		Running:         []RunningCard{},
		Ready:           []IssueCard{},
		Held:            []IssueCard{},
		NeedsEnrichment: []IssueCard{},
		Errors:          []SourceError{},
		DispatchPaused:  in.DispatchPaused,
	}
	var ready []dispatch.Candidate
	var cols columns
	for _, rd := range in.Repos {
		ready = append(ready, classifyIssues(&cols, rd)...)
		cols.running = append(cols.running, pipelineRuns(rd)...)
	}
	for _, c := range dispatch.Order(ready, nil) {
		b.Ready = append(b.Ready, card(c.Issue, c.Owner+"/"+c.Repo, ""))
	}
	b.Running = append(b.Running, cols.running...)
	b.Running = append(b.Running, sessionCards(in)...)
	slices.SortStableFunc(b.Running, func(x, y RunningCard) int { return x.Since.Compare(y.Since) })
	b.Held = sortedCards(cols.held)
	b.NeedsEnrichment = sortedCards(cols.needs)
	return b
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
		case hasAny(i.Labels, dispatch.LabelParked, LabelRoadmap):
		case hasAny(i.Labels, LabelEnrichmentOngoing):
			b.running = append(b.running, RunningCard{
				Kind: KindEnrichment, Repo: repoName(rd), Issue: i.Number, Title: i.Title,
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
// held ones are appended to b.held, anything else is dropped.
func gate(b *columns, rd RepoData, i forge.Issue, active string) []dispatch.Candidate {
	ok, reason := dispatch.Eligible(i, active, rd.PRs)
	if ok {
		return []dispatch.Candidate{{Issue: i, Owner: rd.Owner, Repo: rd.Name, MilestoneDue: milestoneDue(rd.Milestones, i.Milestone)}}
	}
	switch reason {
	case "outside active milestone", "attempts exhausted", "open PR":
		b.held = append(b.held, dated{card(i, repoName(rd), reason), i.Created})
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
		c := RunningCard{Kind: KindPipeline, Repo: repoName(rd), Title: r.Name, URL: r.URL, Status: r.Status, Since: r.Created}
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
		out = append(out, RunningCard{
			Kind: KindSession, Repo: repoForCWD(in.Repos, s.CWD), Title: s.Name,
			Status: s.Status, Since: s.StartedAt, SessionID: s.SessionID, CWD: s.CWD,
		})
	}
	return out
}

// repoForCWD returns the repo whose Path is the longest path-boundary prefix of cwd.
func repoForCWD(repos []RepoData, cwd string) string {
	best, bestLen := "", -1
	c := filepath.Clean(cwd)
	for _, rd := range repos {
		if rd.Path == "" {
			continue
		}
		p := filepath.Clean(rd.Path)
		if (c == p || strings.HasPrefix(c, p+"/")) && len(p) > bestLen {
			best, bestLen = repoName(rd), len(p)
		}
	}
	return best
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
	return !hasAny(labels, dispatch.LabelParked, LabelRoadmap, LabelEnrichmentOngoing,
		dispatch.LabelNeedsEnrichment, dispatch.LabelToBeDefined, dispatch.LabelAIImplement)
}
