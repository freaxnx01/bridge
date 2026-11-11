package dispatch

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// marker wraps a JSON object in a run report shaped like the pipeline's, so the
// parser is exercised on the whole comment body and not just the last line.
func marker(json string) string {
	return "## ai-implement run\n\n- outcome: failed\n[View workflow run](u)\n<!-- ai-run: " + json + " -->"
}

const fullMarkerJSON = `{"v":1,"attempt":2,"counts":true,"retry_attempt":1,` +
	`"class":"task_failure","decision":"stop","status":"ai:failed","subtype":"gate_failed",` +
	`"agent":"claude","model":"claude-opus-5","turns":60,"max_turns":80,"cost_usd":2.341,` +
	`"pr":42,"run_url":"https://x/runs/1","duration_ms":1234}`

func TestParseRunMarker(t *testing.T) {
	full := RunRecord{
		Attempt: 2, Counts: true, RetryAttempt: 1,
		Class: "task_failure", Decision: "stop", Status: "ai:failed", Subtype: "gate_failed",
		Agent: "claude", Model: "claude-opus-5", Turns: 60, CostUSD: 2.341,
		PR: 42, RunURL: "https://x/runs/1",
	}

	cases := []struct {
		name   string
		body   string
		wantOK bool
		want   RunRecord
	}{
		{"full v1 object", marker(fullMarkerJSON), true, full},
		{"trailing blank lines", marker(fullMarkerJSON) + "\n\n  \n", true, full},
		{"marker not on the last non-empty line", marker(fullMarkerJSON) + "\nthanks", false, RunRecord{}},
		{"version 2", marker(`{"v":2,"class":"bug"}`), false, RunRecord{}},
		{"version missing", marker(`{"class":"bug"}`), false, RunRecord{}},
		{"malformed json", marker(`{"v":1,`), false, RunRecord{}},
		{"prefix without spaces", "## run\n<!--ai-run:" + fullMarkerJSON + "-->", false, RunRecord{}},
		// Short enough that the prefix and the suffix overlap on the one space.
		{"empty marker line", "## run\n<!-- ai-run: -->", false, RunRecord{}},
		{"no marker at all", "## ai-implement parked\n\nAttempt cap reached.", false, RunRecord{}},
		{"unknown field is ignored", marker(`{"v":1,"class":"bug","future":1}`), true,
			RunRecord{Class: "bug"}},
		// The pipeline writes a model id containing the comment terminator as JSON
		// escapes, so the raw marker line never carries a stray "-->".
		{"json escapes decode", marker("{\"v\":1,\"model\":\"x--\\u003ey\\u003cz\"}"), true,
			RunRecord{Model: "x-->y<z"}},
		{"paused run: optional fields absent", marker(`{"v":1,"counts":false,"class":"out_of_credits"}`), true,
			RunRecord{Class: "out_of_credits"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseRunMarker(c.body)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (record %+v)", ok, c.wantOK, got)
			}
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestLatestRun(t *testing.T) {
	t0 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	bot := func(id int, created time.Time, body string) forge.Comment {
		return forge.Comment{ID: id, Author: "github-actions[bot]", Body: body, Created: created}
	}
	markerFor := func(class string) string {
		return marker(`{"v":1,"class":"` + class + `"}`)
	}

	cases := []struct {
		name       string
		comments   []forge.Comment
		wantOK     bool
		wantClass  string
		wantPosted time.Time
	}{
		{"no comments", nil, false, "", time.Time{}},
		{"one pipeline comment",
			[]forge.Comment{bot(1, t0, markerFor("bug"))},
			true, "bug", t0},
		{"newest by Created wins",
			[]forge.Comment{
				bot(2, t0.Add(time.Hour), markerFor("transient")),
				bot(1, t0, markerFor("bug")),
			},
			true, "transient", t0.Add(time.Hour)},
		{"equal Created: later slice element wins",
			[]forge.Comment{bot(1, t0, markerFor("bug")), bot(2, t0, markerFor("transient"))},
			true, "transient", t0},
		{"a marker from another author is ignored",
			[]forge.Comment{
				bot(1, t0, markerFor("bug")),
				{ID: 2, Author: "someone", Body: markerFor("transient"), Created: t0.Add(time.Hour)},
			},
			true, "bug", t0},
		{"newest pipeline comment without a marker falls back to the older one",
			[]forge.Comment{
				bot(1, t0, markerFor("bug")),
				bot(2, t0.Add(time.Hour), "## ai-implement parked\n\nAttempt cap reached."),
			},
			true, "bug", t0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LatestRun(c.comments)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (record %+v)", ok, c.wantOK, got)
			}
			if got.Class != c.wantClass {
				t.Errorf("Class = %q, want %q", got.Class, c.wantClass)
			}
			if !got.Posted.Equal(c.wantPosted) {
				t.Errorf("Posted = %v, want %v", got.Posted, c.wantPosted)
			}
		})
	}
}

// runsCase is one input to Runs plus the record order it must produce, shared
// by TestRuns and TestLatestRun_EqualsLastOfRuns so the two contracts cannot
// drift apart.
type runsCase struct {
	name     string
	comments []forge.Comment
	// wantClasses and wantPosted are the records Runs must return, oldest first.
	wantClasses []string
	wantPosted  []time.Time
}

func runsCases() []runsCase {
	t0 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	bot := func(id int, created time.Time, body string) forge.Comment {
		return forge.Comment{ID: id, Author: pipelineReportAuthor, Body: body, Created: created}
	}
	markerFor := func(class string) string { return marker(`{"v":1,"class":"` + class + `"}`) }

	return []runsCase{
		{name: "no comments"},
		{
			name: "out of order markers come back oldest first",
			comments: []forge.Comment{
				bot(3, t0.Add(2*time.Hour), markerFor("bug")),
				bot(1, t0, markerFor("transient")),
				bot(2, t0.Add(time.Hour), markerFor("rate_limit")),
			},
			wantClasses: []string{"transient", "rate_limit", "bug"},
			wantPosted:  []time.Time{t0, t0.Add(time.Hour), t0.Add(2 * time.Hour)},
		},
		{
			name: "equal Created keeps slice order",
			comments: []forge.Comment{
				bot(1, t0, markerFor("bug")),
				bot(2, t0, markerFor("transient")),
			},
			wantClasses: []string{"bug", "transient"},
			wantPosted:  []time.Time{t0, t0},
		},
		{
			name: "a marker from another author is excluded",
			comments: []forge.Comment{
				bot(1, t0, markerFor("bug")),
				{ID: 2, Author: "someone", Body: markerFor("transient"), Created: t0.Add(time.Hour)},
			},
			wantClasses: []string{"bug"},
			wantPosted:  []time.Time{t0},
		},
		{
			name: "a pipeline comment without a marker is excluded",
			comments: []forge.Comment{
				bot(1, t0, "## ai-implement parked\n\nAttempt cap reached."),
			},
		},
	}
}

func TestRuns(t *testing.T) {
	for _, c := range runsCases() {
		t.Run(c.name, func(t *testing.T) {
			got := Runs(c.comments)
			if got == nil {
				t.Fatal("Runs returned nil, want a non-nil slice")
			}
			if len(got) != len(c.wantClasses) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(c.wantClasses), got)
			}
			for i, r := range got {
				if r.Class != c.wantClasses[i] {
					t.Errorf("[%d].Class = %q, want %q", i, r.Class, c.wantClasses[i])
				}
				if !r.Posted.Equal(c.wantPosted[i]) {
					t.Errorf("[%d].Posted = %v, want %v", i, r.Posted, c.wantPosted[i])
				}
			}
		})
	}
}

func TestLatestRun_EqualsLastOfRuns(t *testing.T) {
	for _, c := range runsCases() {
		t.Run(c.name, func(t *testing.T) {
			all := Runs(c.comments)
			got, ok := LatestRun(c.comments)
			if len(all) == 0 {
				if ok || got != (RunRecord{}) {
					t.Fatalf("LatestRun = (%+v, %v), want (zero, false)", got, ok)
				}
				return
			}
			if !ok {
				t.Fatalf("LatestRun ok = false, want true")
			}
			if want := all[len(all)-1]; got != want {
				t.Errorf("LatestRun = %+v, want %+v (last of Runs)", got, want)
			}
		})
	}
}

func TestRunRecord_MarshalJSON_LowerCamelKeys(t *testing.T) {
	r := RunRecord{
		Attempt: 2, Counts: true, RetryAttempt: 1, Class: "task_failure", Decision: "stop",
		Status: "ai:failed", Subtype: "gate_failed", Agent: "claude", Model: "claude-opus-5",
		Turns: 60, CostUSD: 2.341, PR: 42, RunURL: "https://x/runs/1",
		Posted: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"attempt", "counts", "retryAttempt", "class", "decision", "status",
		"subtype", "agent", "model", "turns", "costUsd", "pr", "runUrl", "posted"}
	keys := slices.Sorted(maps.Keys(got))
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}
