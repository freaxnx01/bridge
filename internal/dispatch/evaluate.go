package dispatch

import (
	"slices"
	"time"
)

// WindowMode says how Evaluate treats the schedule.
type WindowMode int

const (
	// GateNow is the timer's tick: a lane outside its window skips.
	GateNow WindowMode = iota
	// GateNextWindow is the cockpit's preview: evaluate at the next instant
	// some lane acts, so a board read in the afternoon shows what tonight's
	// first tick would do rather than a column of "outside lane window".
	GateNextWindow
	// GateNone is a manual tick: `dispatch now` was never window-gated.
	GateNone
)

// TickInput is everything one tick decides from. Everything in it is already
// measured or fetched, which is what keeps Evaluate a pure function the CLI
// and the cockpit can both call.
type TickInput struct {
	Repos  []RepoInput
	Config Config
	State  State
	Gate   GateState
	// Retries are the retry pass's candidates and RetrySkips the rows it
	// decided before the caps ran. Collecting them costs forge reads, so the
	// caller does it; Evaluate only orders and bounds them.
	Retries    []Candidate
	RetrySkips []Decision
	RetryOnly  bool
	UsedUSD    float64 // measured trailing usage; ignored when !UsedKnown
	UsedKnown  bool
	Now        time.Time
	Mode       WindowMode
}

// LaneGate is one configured lane's mode and spent counter.
type LaneGate struct {
	Name       string `json:"name"`
	DryRun     bool   `json:"dryRun"`
	Autonomous bool   `json:"autonomous"`
	Dispatched int    `json:"dispatched"`
	Max        int    `json:"max"` // 0 = unbounded
}

// Gates is every bound in force for a tick, in the shape the cockpit's gate
// strip and the tick log both read.
type Gates struct {
	At          time.Time      `json:"at"`         // the instant evaluated
	WindowOpen  bool           `json:"windowOpen"` // at Now, not At
	NextOpening time.Time      `json:"nextOpening,omitzero"`
	RungOn      bool           `json:"rungOn"`
	UsedUSD     float64        `json:"usedUsd"`
	AllowedUSD  float64        `json:"allowedUsd"` // WindowBudgetUSD * DaytimeCap
	BudgetKnown bool           `json:"budgetKnown"`
	NightCount  int            `json:"nightCount"`
	NightCap    int            `json:"nightCap"`
	GlobalOpen  int            `json:"globalOpen"`
	GlobalCap   int            `json:"globalCap"`
	OpenByRepo  map[string]int `json:"openByRepo,omitempty"`
	Paused      bool           `json:"paused"`
	LastTick    time.Time      `json:"lastTick,omitzero"`
	Lanes       []LaneGate     `json:"lanes,omitempty"`
}

// Tick is one evaluated tick: its decisions in ladder order, and the gates
// that produced them.
type Tick struct {
	Decisions []Decision
	Gates     Gates
}

// Evaluate decides one tick. It reads no clock beyond in.Now, touches no
// filesystem and mutates nothing it is given, so the CLI tick and the
// cockpit's live preview cannot drift apart.
func Evaluate(in TickInput) Tick {
	at := in.Now
	if in.Mode == GateNextWindow {
		at = NextActingInstant(in.Config, in.Now)
	}
	timing := ResolveTiming(in.Config, at)

	ordered := orderedCandidates(in)
	acting, outOfWindow := partitionFor(in.Mode, ordered, in.Config, at)

	budget := NewBudgetState(in.Config.Budget, timing.RungOn, in.UsedUSD, in.UsedKnown)
	openByRepo, globalOpen := CountOpenAgentPRs(in.Repos, func(repo string) bool {
		lane, _ := ResolveLane(in.Config.Lanes, repo, in.Gate)
		return lane.Autonomous
	})
	nightCount := in.State.DispatchesSince(NightStartForReport(in.Config, timing, at))

	decisions := ApplyCaps(acting, in.Config, Counts{
		OpenPRsByRepo:     openByRepo,
		GlobalOpen:        globalOpen,
		DispatchedTonight: nightCount,
		NightCapApplies:   timing.NightCapApplies,
		DispatchedByLane:  LaneCounts(in.State, in.Config, at),
	}, budget)
	decisions = MergeInOrder(ordered, decisions, outOfWindow)
	// The retry pass's own skips never reached ApplyCaps, so they are not in
	// `ordered` either. They are reported in every mode and applied in none.
	decisions = append(decisions, lanedSkips(in)...)
	attachRanks(decisions, ordered, in.Config.RepoPriority)

	return Tick{
		Decisions: decisions,
		Gates: Gates{
			At:          at,
			WindowOpen:  AnyLaneActs(in.Config, in.Now),
			NextOpening: nextOpening(in.Config, in.Now),
			RungOn:      budget.Enabled,
			UsedUSD:     budget.UsedUSD,
			AllowedUSD:  budget.LimitUSD,
			BudgetKnown: !budget.Unknown,
			NightCount:  nightCount,
			NightCap:    in.Config.Limits.MaxDispatchesPerNight,
			GlobalOpen:  globalOpen,
			GlobalCap:   in.Config.Limits.GlobalOpenPRs,
			OpenByRepo:  openByRepo,
			Paused:      in.State.Paused,
			LastTick:    in.State.LastTick,
			Lanes:       laneGates(in.Config, in.State, at),
		},
	}
}

// orderedCandidates is the tick's work in ladder order: the retry pass first,
// then the new dispatches, each ordered on its own. Retries lead because a
// failed issue already carries a run's worth of sunk cost, so finishing it
// beats starting something new under the same caps.
func orderedCandidates(in TickInput) []Candidate {
	retries := slices.Clone(in.Retries)
	AssignLanes(retries, in.Config.Lanes, in.Gate)
	ordered := Order(retries, in.Config.RepoPriority)
	if in.RetryOnly {
		return ordered
	}
	fresh := CollectCandidates(in.Repos)
	AssignLanes(fresh, in.Config.Lanes, in.Gate)
	return append(ordered, Order(fresh, in.Config.RepoPriority)...)
}

// lanedSkips resolves the retry skips' lanes on a copy, so a caller's slice is
// never written through.
func lanedSkips(in TickInput) []Decision {
	out := slices.Clone(in.RetrySkips)
	for i := range out {
		out[i].Candidate.Lane, out[i].Candidate.LaneReason =
			ResolveLane(in.Config.Lanes, out[i].Candidate.Repo, in.Gate)
	}
	return out
}

// partitionFor splits candidates into those whose lane acts at `at` and skip
// decisions for the rest. A manual tick skips the split entirely: `dispatch
// now` was never window-gated, and partitioning it would turn a documented
// schedule gap into a tick that dispatches nothing.
func partitionFor(mode WindowMode, ordered []Candidate, cfg Config, at time.Time) ([]Candidate, []Decision) {
	if mode == GateNone {
		return ordered, nil
	}
	return PartitionByWindow(ordered, cfg.Schedule, at)
}

// attachRanks copies each candidate's ladder reading onto its decision. A row
// that never entered the ladder — a retry skipped before the caps ran — keeps
// the zero Rank, which is how a reader tells the two apart.
func attachRanks(decisions []Decision, ordered []Candidate, repoPriority []string) {
	type key struct {
		repo   string
		number int
	}
	ranks := Explain(ordered, repoPriority)
	byKey := make(map[key]Rank, len(ranks))
	for i, c := range ordered {
		byKey[key{c.Repo, c.Issue.Number}] = ranks[i]
	}
	for i := range decisions {
		k := key{decisions[i].Candidate.Repo, decisions[i].Candidate.Issue.Number}
		if r, ok := byKey[k]; ok {
			decisions[i].Rank = r
		}
	}
}

func laneGates(cfg Config, state State, at time.Time) []LaneGate {
	if len(cfg.Lanes) == 0 {
		return nil
	}
	out := make([]LaneGate, 0, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		out = append(out, LaneGate{
			Name:       l.Name,
			DryRun:     l.DryRun,
			Autonomous: l.Autonomous,
			Dispatched: state.DispatchesInLane(l.Name, l.WindowStart(cfg.Schedule, at)),
			Max:        l.Limits.MaxDispatches,
		})
	}
	return out
}

// nextOpening is when some lane next starts acting, zero while one already is.
func nextOpening(cfg Config, now time.Time) time.Time {
	next := NextActingInstant(cfg, now)
	if next.Equal(now) {
		// Either a lane acts right now, or none acts within the next day at
		// all. Neither is an opening to announce.
		return time.Time{}
	}
	return next
}

// nextInstantScan bounds the forward scan: a full day of minutes, which is
// exact for the minute-granular HH:MM spans a schedule is written in.
const nextInstantScan = 24 * 60

// NextActingInstant is the next minute some lane acts, now included. It falls
// back to now when nothing acts within a day — a preview still has to answer.
func NextActingInstant(cfg Config, now time.Time) time.Time {
	if AnyLaneActs(cfg, now) {
		return now
	}
	start := now.Truncate(time.Minute)
	for i := 1; i <= nextInstantScan; i++ {
		if t := start.Add(time.Duration(i) * time.Minute); AnyLaneActs(cfg, t) {
			return t
		}
	}
	return now
}
