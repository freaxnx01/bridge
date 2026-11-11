package dispatch

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// pipelineReportAuthor is the only identity whose run marker is trusted — the
// account agent-workflow's "Post run report" step posts as. Its ADR-021
// consumer trust rule: a marker from anyone else is display-only text, because
// any user can copy one into a comment.
const pipelineReportAuthor = "github-actions[bot]"

const (
	markerPrefix  = "<!-- ai-run: "
	markerSuffix  = " -->"
	markerVersion = 1
)

// RunRecord is one pipeline run, decoded from the run marker the pipeline
// appends to its run report (agent-workflow ADR-021).
// The JSON tags are the cockpit API's lower-camel shape, not the marker's
// snake_case: the marker is parsed through runMarker, this is what bridge
// serves.
type RunRecord struct {
	Attempt      int       `json:"attempt"` // lifetime attempt number; 0 = absent (run did not count, or cap step skipped)
	Counts       bool      `json:"counts"`
	RetryAttempt int       `json:"retryAttempt"`
	Class        string    `json:"class"`    // classify-failure.sh class; "" = absent
	Decision     string    `json:"decision"` // "retry" | "stop" | ""
	Status       string    `json:"status"`   // "ai:done" | "ai:failed" | "ai:partial" | ""
	Subtype      string    `json:"subtype"`
	Agent        string    `json:"agent"`
	Model        string    `json:"model"`
	Turns        int       `json:"turns"`
	CostUSD      float64   `json:"costUsd"`
	PR           int       `json:"pr"`
	RunURL       string    `json:"runUrl"`
	Posted       time.Time `json:"posted"` // Created of the comment the marker came from
}

// runMarker is the wire shape of the marker's JSON. V is a pointer so an absent
// version is distinguishable from version zero. Fields the pipeline may add
// later (max_turns, duration_ms, plan_coverage, …) are ignored on purpose:
// additive fields are not a version bump.
type runMarker struct {
	V            *int    `json:"v"`
	Attempt      int     `json:"attempt"`
	Counts       bool    `json:"counts"`
	RetryAttempt int     `json:"retry_attempt"`
	Class        string  `json:"class"`
	Decision     string  `json:"decision"`
	Status       string  `json:"status"`
	Subtype      string  `json:"subtype"`
	Agent        string  `json:"agent"`
	Model        string  `json:"model"`
	Turns        int     `json:"turns"`
	CostUSD      float64 `json:"cost_usd"`
	PR           int     `json:"pr"`
	RunURL       string  `json:"run_url"`
}

// ParseRunMarker decodes the run marker from a comment body. The marker must be
// the body's last non-empty line and declare version 1; anything else reports
// false, so an unparseable report is treated as no report at all.
func ParseRunMarker(body string) (RunRecord, bool) {
	line := lastNonEmptyLine(body)
	if !strings.HasPrefix(line, markerPrefix) || !strings.HasSuffix(line, markerSuffix) {
		return RunRecord{}, false
	}
	// A line too short to hold both affixes satisfies both checks by overlapping
	// them (`<!-- ai-run: -->` shares its space), so measure before slicing.
	if len(line) < len(markerPrefix)+len(markerSuffix) {
		return RunRecord{}, false
	}
	raw := line[len(markerPrefix) : len(line)-len(markerSuffix)]

	var m runMarker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return RunRecord{}, false
	}
	if m.V == nil || *m.V != markerVersion {
		return RunRecord{}, false
	}
	return RunRecord{
		Attempt: m.Attempt, Counts: m.Counts, RetryAttempt: m.RetryAttempt,
		Class: m.Class, Decision: m.Decision, Status: m.Status, Subtype: m.Subtype,
		Agent: m.Agent, Model: m.Model, Turns: m.Turns, CostUSD: m.CostUSD,
		PR: m.PR, RunURL: m.RunURL,
	}, true
}

// Runs returns every parseable run marker among the pipeline's own comments,
// oldest first, each with Posted set to its comment's creation time. Equal
// Created times keep the order the forge listed the comments in.
func Runs(comments []forge.Comment) []RunRecord {
	out := make([]RunRecord, 0, len(comments))
	for _, c := range comments {
		if c.Author != pipelineReportAuthor {
			continue
		}
		r, ok := ParseRunMarker(c.Body)
		if !ok {
			continue
		}
		r.Posted = c.Created
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(x, y RunRecord) int { return x.Posted.Compare(y.Posted) })
	return out
}

// LatestRun returns the newest parseable run marker among the pipeline's own
// comments, with Posted set to that comment's creation time. Ties on Created go
// to the later element, which is the order the forges list comments in.
func LatestRun(comments []forge.Comment) (RunRecord, bool) {
	all := Runs(comments)
	if len(all) == 0 {
		return RunRecord{}, false
	}
	return all[len(all)-1], true
}

func lastNonEmptyLine(body string) string {
	lines := strings.Split(body, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
