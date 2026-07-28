package dispatch

import "fmt"

// Decision is one candidate's outcome, carrying the reason so --dry-run can
// explain every skip.
type Decision struct {
	Candidate Candidate
	Dispatch  bool
	Reason    string
}

// ApplyCaps walks an ordered candidate list and marks each one dispatch or
// skip, tightening three independent bounds as it goes:
//
//	per-repo    — avoids conflicting concurrent PRs in one repo
//	global      — the operator's review capacity
//	nightly     — bounds unattended spend, which the WIP cap alone cannot
//
// openPRsByRepo and globalOpen are the counts that already exist before this
// tick; dispatchedTonight is how many this night has already produced.
func ApplyCaps(ordered []Candidate, cfg Config, openPRsByRepo map[string]int, globalOpen, dispatchedTonight int) []Decision {
	perRepo := make(map[string]int, len(openPRsByRepo))
	for k, v := range openPRsByRepo {
		perRepo[k] = v
	}
	global := globalOpen
	night := dispatchedTonight

	out := make([]Decision, 0, len(ordered))
	for _, c := range ordered {
		limit := cfg.LimitFor(c.Repo)
		switch {
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
			out = append(out, Decision{c, true, ""})
		}
	}
	return out
}
