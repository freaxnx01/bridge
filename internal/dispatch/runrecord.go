package dispatch

import (
	"encoding/json"
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
type RunRecord struct {
	Attempt      int // lifetime attempt number; 0 = absent (run did not count, or cap step skipped)
	Counts       bool
	RetryAttempt int
	Class        string // classify-failure.sh class; "" = absent
	Decision     string // "retry" | "stop" | ""
	Status       string // "ai:done" | "ai:failed" | "ai:partial" | ""
	Subtype      string
	Agent, Model string
	Turns        int
	CostUSD      float64
	PR           int
	RunURL       string
	Posted       time.Time // Created of the comment the marker came from
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

// LatestRun returns the newest parseable run marker among the pipeline's own
// comments, with Posted set to that comment's creation time. Ties on Created go
// to the later element, which is the order the forges list comments in.
func LatestRun(comments []forge.Comment) (RunRecord, bool) {
	var latest RunRecord
	found := false
	for _, c := range comments {
		if c.Author != pipelineReportAuthor {
			continue
		}
		if found && c.Created.Before(latest.Posted) {
			continue
		}
		r, ok := ParseRunMarker(c.Body)
		if !ok {
			continue
		}
		r.Posted = c.Created
		latest, found = r, true
	}
	return latest, found
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
