package dispatch

import (
	"fmt"
	"time"
)

// Decision is one candidate's outcome, carrying the reason so --dry-run can
// explain every skip.
type Decision struct {
	Candidate Candidate
	Dispatch  bool
	Reason    string
}

// Counts are the tick's pre-existing counts: how many agent PRs are already
// open per repo and in total, and how many dispatches this night has produced.
type Counts struct {
	OpenPRsByRepo     map[string]int
	GlobalOpen        int
	DispatchedTonight int
	// NightCapApplies arms the nightly ceiling. It bounds *unattended* spend,
	// so it belongs only to a window whose budget rung is off — during a rung
	// window the budget itself is the bound, and applying both would refuse
	// daytime work using the night's spent counter.
	NightCapApplies bool
	// DispatchedByLane is what each lane's current window occurrence has
	// already spent.
	DispatchedByLane map[string]int
}

// ApplyCaps walks an ordered candidate list and marks each one dispatch or
// skip, tightening bounds as it goes:
//
//	budget      — the operator's remaining subscription headroom (daytime only)
//	lane        — a lane's own dispatch ceiling for its current window occurrence
//	nightly     — bounds unattended spend, which the WIP cap alone cannot
//	global      — the operator's review capacity
//	per-repo    — avoids conflicting concurrent PRs in one repo
//
// An autonomous lane's candidates are exempt from the nightly and global caps
// — nobody reviews their PRs, so those bounds do not apply — but still respect
// the lane's own ceiling and the per-repo limit. A dry-run lane advances only
// its own private counter and leaves every shared counter (per-repo, global,
// nightly, budget) exactly as it found it, so the observation week never
// throttles the lanes it is meant to observe.
//
// The budget rung is checked first because it is the only bound protecting the
// human rather than the machine: once the window is spent, nothing else about
// the candidate matters.
func ApplyCaps(ordered []Candidate, cfg Config, counts Counts, budget BudgetState) []Decision {
	perRepo := make(map[string]int, len(counts.OpenPRsByRepo))
	for k, v := range counts.OpenPRsByRepo {
		perRepo[k] = v
	}
	byLane := make(map[string]int, len(counts.DispatchedByLane))
	for k, v := range counts.DispatchedByLane {
		byLane[k] = v
	}
	global := counts.GlobalOpen
	night := counts.DispatchedTonight
	spent := budget.UsedUSD

	out := make([]Decision, 0, len(ordered))
	for _, c := range ordered {
		lane := c.Lane
		limit := effectiveRepoLimit(cfg, lane, c.Repo)
		laneCap := lane.Limits.MaxDispatches
		switch {
		case budget.Enabled && budget.Unknown:
			out = append(out, Decision{c, false, "budget-unknown"})
		case budget.Enabled && spent+budget.PerRunUSD > budget.LimitUSD:
			out = append(out, Decision{c, false,
				fmt.Sprintf("budget-exhausted %.2f/%.2f USD", spent, budget.LimitUSD)})
		case laneCap > 0 && byLane[lane.Name] >= laneCap:
			out = append(out, Decision{c, false,
				fmt.Sprintf("lane cap %d/%d (%s)", byLane[lane.Name], laneCap, lane.Name)})
		case counts.NightCapApplies && !lane.Autonomous && night >= cfg.Limits.MaxDispatchesPerNight:
			out = append(out, Decision{c, false,
				fmt.Sprintf("night cap %d/%d", night, cfg.Limits.MaxDispatchesPerNight)})
		case !lane.Autonomous && global >= cfg.Limits.GlobalOpenPRs:
			out = append(out, Decision{c, false,
				fmt.Sprintf("global cap %d/%d", global, cfg.Limits.GlobalOpenPRs)})
		case perRepo[c.Repo] >= limit:
			out = append(out, Decision{c, false,
				fmt.Sprintf("repo at WIP %d/%d", perRepo[c.Repo], limit)})
		default:
			// The lane's own counter is private to it, so a dry-run lane still
			// advances it and still hits its own ceiling. Everything else here
			// is shared, and a dry-run lane must leave it exactly as it found
			// it — it dispatches nothing, so it costs nothing.
			byLane[lane.Name]++
			if !lane.DryRun {
				perRepo[c.Repo]++
				spent += budget.PerRunUSD
				if !lane.Autonomous {
					global++
					night++
				}
			}
			out = append(out, Decision{c, true, ""})
		}
	}
	return out
}

// effectiveRepoLimit resolves the per-repo WIP limit: the lane's override for
// that repo, then the lane's per_repo, then the top-level config. A lane states
// only what it changes.
func effectiveRepoLimit(cfg Config, lane Lane, repo string) int {
	if n, ok := lane.Limits.Overrides[repo]; ok {
		return n
	}
	if lane.Limits.PerRepo > 0 {
		return lane.Limits.PerRepo
	}
	return cfg.LimitFor(repo)
}

// PartitionByWindow splits ordered candidates into those whose lane acts at
// now and skip decisions for the rest. It is separate from ApplyCaps so the
// cap walk keeps no clock: the schedule is read once, here.
func PartitionByWindow(ordered []Candidate, s Schedule, now time.Time) ([]Candidate, []Decision) {
	var act []Candidate
	var skipped []Decision
	for _, c := range ordered {
		if c.Lane.InWindow(s, now) {
			act = append(act, c)
			continue
		}
		skipped = append(skipped, Decision{c, false,
			fmt.Sprintf("outside lane window (%s)", c.Lane.Name)})
	}
	return act, skipped
}
