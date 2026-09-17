package dispatch

import "fmt"

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
}

// ApplyCaps walks an ordered candidate list and marks each one dispatch or
// skip, tightening four independent bounds as it goes:
//
//	budget      — the operator's remaining subscription headroom (daytime only)
//	per-repo    — avoids conflicting concurrent PRs in one repo
//	global      — the operator's review capacity
//	nightly     — bounds unattended spend, which the WIP cap alone cannot
//
// The budget rung is checked first because it is the only bound protecting the
// human rather than the machine: once the window is spent, nothing else about
// the candidate matters.
func ApplyCaps(ordered []Candidate, cfg Config, counts Counts, budget BudgetState) []Decision {
	perRepo := make(map[string]int, len(counts.OpenPRsByRepo))
	for k, v := range counts.OpenPRsByRepo {
		perRepo[k] = v
	}
	global := counts.GlobalOpen
	night := counts.DispatchedTonight
	spent := budget.UsedUSD

	out := make([]Decision, 0, len(ordered))
	for _, c := range ordered {
		limit := cfg.LimitFor(c.Repo)
		switch {
		case budget.Enabled && budget.Unknown:
			out = append(out, Decision{c, false, "budget-unknown"})
		case budget.Enabled && spent+budget.PerRunUSD > budget.LimitUSD:
			out = append(out, Decision{c, false,
				fmt.Sprintf("budget-exhausted %.2f/%.2f USD", spent, budget.LimitUSD)})
		case night >= cfg.Limits.MaxDispatchesPerNight:
			out = append(out, Decision{c, false,
				fmt.Sprintf("night cap %d/%d", night, cfg.Limits.MaxDispatchesPerNight)})
		case global >= cfg.Limits.GlobalOpenPRs:
			out = append(out, Decision{c, false,
				fmt.Sprintf("global cap %d/%d", global, cfg.Limits.GlobalOpenPRs)})
		case perRepo[c.Repo] >= limit:
			out = append(out, Decision{c, false,
				fmt.Sprintf("repo at WIP %d/%d", perRepo[c.Repo], limit)})
		default:
			perRepo[c.Repo]++
			global++
			night++
			spent += budget.PerRunUSD
			out = append(out, Decision{c, true, ""})
		}
	}
	return out
}
