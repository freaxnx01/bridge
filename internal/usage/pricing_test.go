package usage

import "testing"

func TestCostOfPricesAllFourTerms(t *testing.T) {
	p := Pricing{"opus": {Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	// 1M of each term: 15 + 75 + 1.5 + 18.75
	got := p.CostOf("opus", Tokens{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite: 1e6})
	if diff := got - 110.25; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("want 110.25, got %v", got)
	}
}

func TestRateForMatchesExactThenFamily(t *testing.T) {
	p := DefaultPricing()
	p["claude-opus-4-7"] = Rate{Input: 99}

	if got := p.RateFor("claude-opus-4-7").Input; got != 99 {
		t.Errorf("exact match must win, got %v", got)
	}
	// No exact entry: the family substring decides.
	if got := p.RateFor("claude-sonnet-5").Input; got != DefaultPricing()["sonnet"].Input {
		t.Errorf("family match: %v", got)
	}
}

func TestRateForUnknownModelFallsBackToMostExpensive(t *testing.T) {
	p := DefaultPricing()
	want := p["opus"]
	if got := p.RateFor("some-future-model"); got != want {
		t.Errorf("unknown model must price at the most expensive known rate, got %+v", got)
	}
}

func TestMergeOverridesOnlyNamedModels(t *testing.T) {
	p := DefaultPricing().Merge(map[string]Rate{"opus": {Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}})

	if p["opus"].Input != 1 {
		t.Errorf("override not applied: %+v", p["opus"])
	}
	if p["haiku"] != DefaultPricing()["haiku"] {
		t.Errorf("unnamed model must keep its default: %+v", p["haiku"])
	}
}

func TestMergeDoesNotMutateReceiver(t *testing.T) {
	base := DefaultPricing()
	base.Merge(map[string]Rate{"opus": {Input: 1}})
	if base["opus"].Input == 1 {
		t.Error("Merge must not mutate the receiver")
	}
}

// budget.pricing is operator-configurable, so the unknown-model fallback must
// not be fooled by an override whose cost sits in the cache terms.
func TestRateForUnknownModelRanksOnAllFourTerms(t *testing.T) {
	p := Pricing{
		"cheap-output": {Input: 200, Output: 1, CacheRead: 200, CacheWrite: 200},
		"rich-output":  {Input: 1, Output: 80, CacheRead: 1, CacheWrite: 1},
	}
	got := p.RateFor("some-future-model")
	if got != p["cheap-output"] {
		t.Errorf("want the genuinely most expensive rate (601 total), got %+v", got)
	}
}
