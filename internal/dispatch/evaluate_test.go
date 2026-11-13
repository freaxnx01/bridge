package dispatch

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// evalRepo is two eligible issues in one repo, which with per_repo 1 is the
// smallest input that produces one dispatch and one cap skip.
func evalRepo() []RepoInput {
	body := "a real description\n## Implementation Plan\n- step"
	return []RepoInput{{
		Forge: "github", Owner: "freaxnx01", Name: "bridge",
		Issues: []forge.Issue{
			{Number: 1, Title: "first", Body: body, Labels: []string{"bug"}, Created: day("2026-10-01")},
			{Number: 2, Title: "second", Body: body, Labels: []string{"enhancement"}, Created: day("2026-10-02")},
		},
	}}
}

func nightOnlyConfig() Config {
	cfg := DefaultConfig()
	cfg.Schedule = Schedule{Windows: []Window{{Span: Span{From: "18:00", To: "07:00"}}}}
	return cfg
}

func tickAt(h, m int) time.Time { return time.Date(2026, 11, 12, h, m, 0, 0, time.Local) }

func TestEvaluate_GateNowOutsideWindows_SkipsOutsideWindow(t *testing.T) {
	tick := Evaluate(TickInput{
		Repos: evalRepo(), Config: nightOnlyConfig(), Now: tickAt(14, 0), Mode: GateNow,
	})

	if len(tick.Decisions) != 2 {
		t.Fatalf("got %d decisions", len(tick.Decisions))
	}
	for _, d := range tick.Decisions {
		if d.Dispatch {
			t.Errorf("#%d dispatched outside every window", d.Candidate.Issue.Number)
		}
		if !strings.Contains(d.Reason, "outside") {
			t.Errorf("#%d reason = %q", d.Candidate.Issue.Number, d.Reason)
		}
	}
}

func TestEvaluate_GateNextWindowAtAfternoon_EvaluatesAtEvening(t *testing.T) {
	tick := Evaluate(TickInput{
		Repos: evalRepo(), Config: nightOnlyConfig(), Now: tickAt(14, 0), Mode: GateNextWindow,
	})

	if !tick.Gates.At.Equal(tickAt(18, 0)) {
		t.Errorf("Gates.At = %v, want %v", tick.Gates.At, tickAt(18, 0))
	}
	if tick.Gates.WindowOpen {
		t.Error("WindowOpen must describe now, not the instant evaluated")
	}
	if !tick.Gates.NextOpening.Equal(tickAt(18, 0)) {
		t.Errorf("NextOpening = %v", tick.Gates.NextOpening)
	}
	if len(tick.Decisions) != 2 {
		t.Fatalf("got %d decisions", len(tick.Decisions))
	}
	if !tick.Decisions[0].Dispatch {
		t.Errorf("first decision: %+v", tick.Decisions[0])
	}
	if tick.Decisions[1].Dispatch || !strings.Contains(tick.Decisions[1].Reason, "WIP") {
		t.Errorf("second decision: %+v", tick.Decisions[1])
	}
}

func TestEvaluate_GateNextWindowInsideWindow_EvaluatesAtNow(t *testing.T) {
	now := tickAt(22, 0)

	tick := Evaluate(TickInput{
		Repos: evalRepo(), Config: nightOnlyConfig(), Now: now, Mode: GateNextWindow,
	})

	if !tick.Gates.At.Equal(now) {
		t.Errorf("Gates.At = %v, want now %v", tick.Gates.At, now)
	}
	if !tick.Gates.WindowOpen {
		t.Error("WindowOpen = false inside a window")
	}
	if !tick.Gates.NextOpening.IsZero() {
		t.Errorf("NextOpening = %v, want zero while open", tick.Gates.NextOpening)
	}
}

func TestEvaluate_RungExhausted_SkipsWithBudgetReason(t *testing.T) {
	tick := Evaluate(TickInput{
		Repos: evalRepo(), Config: DefaultConfig(),
		UsedUSD: 20, UsedKnown: true, Now: tickAt(14, 0), Mode: GateNow,
	})

	// 12.0 * 0.80 is not exactly 9.6 in binary floating point.
	if got := tick.Gates.AllowedUSD; math.Abs(got-9.6) > 1e-9 {
		t.Errorf("AllowedUSD = %v, want 9.6", got)
	}
	if !tick.Gates.RungOn {
		t.Error("RungOn = false inside the guarded window")
	}
	for _, d := range tick.Decisions {
		if !strings.Contains(d.Reason, "budget-exhausted 20.00/9.60 USD") {
			t.Errorf("#%d reason = %q", d.Candidate.Issue.Number, d.Reason)
		}
	}
}

func TestEvaluate_DecisionsCarryRank(t *testing.T) {
	tick := Evaluate(TickInput{
		Repos: evalRepo(), Config: nightOnlyConfig(), Now: tickAt(22, 0), Mode: GateNone,
	})

	if len(tick.Decisions) != 2 {
		t.Fatalf("got %d decisions", len(tick.Decisions))
	}
	if tick.Decisions[0].Rank.Position != 1 || tick.Decisions[1].Rank.Position != 2 {
		t.Errorf("positions: %d, %d",
			tick.Decisions[0].Rank.Position, tick.Decisions[1].Rank.Position)
	}
	if tick.Decisions[0].Rank.Reason != "type: bug vs feat" {
		t.Errorf("Rank.Reason = %q", tick.Decisions[0].Rank.Reason)
	}
	if tick.Decisions[0].Rank.Ladder.Type != "bug" {
		t.Errorf("Ladder = %+v", tick.Decisions[0].Rank.Ladder)
	}
}

func TestEvaluate_PausedState_IsReportedInTheGates(t *testing.T) {
	lastTick := tickAt(13, 30)

	tick := Evaluate(TickInput{
		Repos: evalRepo(), Config: nightOnlyConfig(),
		State: State{Paused: true, LastTick: lastTick},
		Now:   tickAt(22, 0), Mode: GateNone,
	})

	if !tick.Gates.Paused || !tick.Gates.LastTick.Equal(lastTick) {
		t.Errorf("gates = %+v", tick.Gates)
	}
}

func TestEvaluate_Lanes_AreReportedWithTheirSpentCounters(t *testing.T) {
	cfg := nightOnlyConfig()
	lane := Lane{Name: "auto", Repos: []string{"bridge"}, DryRun: true,
		Windows: []Span{{From: "00:00", To: "00:00"}}, Limits: LaneLimits{MaxDispatches: 12}}
	cfg.Lanes = []Lane{lane}
	now := tickAt(22, 0)
	state := State{}
	state.RecordLaneDispatch("auto", lane.WindowStart(cfg.Schedule, now), now, 3)

	tick := Evaluate(TickInput{Repos: evalRepo(), Config: cfg, State: state, Now: now, Mode: GateNone})

	want := []LaneGate{{Name: "auto", DryRun: true, Dispatched: 3, Max: 12}}
	if !reflect.DeepEqual(tick.Gates.Lanes, want) {
		t.Errorf("lanes = %+v, want %+v", tick.Gates.Lanes, want)
	}
}

func TestEvaluate_DoesNotMutateInputState(t *testing.T) {
	in := TickInput{
		Repos: evalRepo(), Config: DefaultConfig(),
		State: State{DispatchedTonight: 2, Lanes: map[string]LaneState{"auto": {Dispatched: 1}}},
		Now:   tickAt(22, 0), Mode: GateNone,
	}
	before := State{DispatchedTonight: 2, Lanes: map[string]LaneState{"auto": {Dispatched: 1}}}

	Evaluate(in)

	if !reflect.DeepEqual(in.State, before) {
		t.Errorf("Evaluate mutated the state: %+v, want %+v", in.State, before)
	}
}

func TestNextActingInstant_LaneActsNow_ReturnsNow(t *testing.T) {
	now := tickAt(22, 0)

	if got := NextActingInstant(nightOnlyConfig(), now); !got.Equal(now) {
		t.Errorf("got %v want %v", got, now)
	}
}

func TestNextActingInstant_NothingActsWithin24h_ReturnsNow(t *testing.T) {
	cfg := DefaultConfig()
	// A schedule nothing can ever satisfy: the preview still has to answer.
	cfg.Schedule = Schedule{Windows: []Window{{Span: Span{From: "bogus", To: "bogus"}}}}
	now := tickAt(14, 0)

	if got := NextActingInstant(cfg, now); !got.Equal(now) {
		t.Errorf("got %v want %v", got, now)
	}
}

// The window gate is --auto only. `bridge dispatch now` is the operator asking
// for a tick, and before lanes it ran regardless of the schedule; a schedule
// with a gap (documented in docs/dispatch.md) must keep behaving that way.
func TestEvaluate_GateNoneInAScheduleGap_StillDecidesEveryCandidate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Schedule = Schedule{Windows: []Window{{Span: Span{From: "03:00", To: "03:01"}}}}
	now := tickAt(13, 0) // outside that window

	none := Evaluate(TickInput{Repos: evalRepo(), Config: cfg, Now: now, Mode: GateNone})
	if !none.Decisions[0].Dispatch {
		t.Errorf("manual tick must consider every candidate: %+v", none.Decisions[0])
	}

	auto := Evaluate(TickInput{Repos: evalRepo(), Config: cfg, Now: now, Mode: GateNow})
	for _, d := range auto.Decisions {
		if d.Dispatch || !strings.Contains(d.Reason, "outside lane window") {
			t.Errorf("--auto must honour the window: %+v", d)
		}
	}
}
