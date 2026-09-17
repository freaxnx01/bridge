package dispatch

// BudgetState is the tick's already-measured budget position. Measuring is the
// caller's job — keeping it out of here is what lets ApplyCaps stay a pure
// function with no clock and no filesystem.
type BudgetState struct {
	Enabled   bool    // the current window turns the rung on
	Unknown   bool    // usage could not be measured — refuse everything
	UsedUSD   float64 // trailing-window consumption, interactive + pipeline
	LimitUSD  float64 // WindowBudgetUSD * DaytimeCap
	PerRunUSD float64 // projected cost of one dispatched run
}

// NewBudgetState builds the rung's input for one tick.
//
// usedKnown false means the measurement failed. That is not the same as zero
// used: the rung exists to protect the operator's headroom, so unreadable
// usage must block rather than wave work through. Nonsensical configuration
// (no budget, no cap, no per-run estimate) fails closed for the same reason.
func NewBudgetState(b Budget, enabled bool, usedUSD float64, usedKnown bool) BudgetState {
	s := BudgetState{
		Enabled:   enabled,
		UsedUSD:   usedUSD,
		LimitUSD:  b.WindowBudgetUSD * b.DaytimeCap,
		PerRunUSD: b.MeanRunCostUSD,
	}
	s.Unknown = !usedKnown ||
		b.WindowBudgetUSD <= 0 ||
		b.DaytimeCap <= 0 ||
		b.MeanRunCostUSD <= 0
	return s
}
