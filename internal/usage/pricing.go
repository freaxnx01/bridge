// Package usage measures Claude subscription consumption over a trailing
// window. It has two sources: Claude Code's local transcripts (interactive
// work) and a ledger of pipeline runs bridge dispatched itself.
//
// Everything here except ScanTranscripts, LoadLedger and WriteLedger is a pure
// function over plain structs — no network, no clock, no filesystem — which is
// what makes the arithmetic table-testable.
package usage

import (
	"sort"
	"strings"
)

// Tokens is one turn's token breakdown, mirroring the shape Claude Code writes
// into message.usage.
type Tokens struct {
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
}

// Rate is a model's price in USD per million tokens, one term per token class.
// Cache reads and cache writes are priced separately on purpose: a typical turn
// reads far more cached tokens than fresh input, so folding them into a single
// per-token rate is wrong by an order of magnitude.
type Rate struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// Pricing maps a model key to its Rate. A key is either a full model id
// ("claude-opus-4-7") or a family name ("opus") matched as a substring, so a
// newly released dated model id prices correctly without a config edit.
type Pricing map[string]Rate

// DefaultPricing is the compiled-in table. Families only: exact model ids are
// left to config overrides, so this table does not go stale on every release.
func DefaultPricing() Pricing {
	return Pricing{
		"opus":   {Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75},
		"sonnet": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"haiku":  {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25},
	}
}

// Merge returns a copy of p with override's entries applied. The receiver is
// left untouched so a caller can keep the defaults around.
func (p Pricing) Merge(override map[string]Rate) Pricing {
	out := make(Pricing, len(p)+len(override))
	for k, v := range p {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// RateFor resolves a model id to a rate: exact key, then family substring, then
// the most expensive known rate. The last step is deliberate — an unrecognised
// model must over-estimate rather than under-estimate, so it tightens the
// budget rung instead of quietly opening it.
func (p Pricing) RateFor(model string) Rate {
	if r, ok := p[model]; ok {
		return r
	}
	for _, k := range sortedKeys(p) {
		if k != "" && strings.Contains(model, k) {
			return p[k]
		}
	}
	return mostExpensive(p)
}

// CostOf prices one turn in USD. Rates are per million tokens.
func (p Pricing) CostOf(model string, t Tokens) float64 {
	r := p.RateFor(model)
	return (float64(t.Input)*r.Input +
		float64(t.Output)*r.Output +
		float64(t.CacheRead)*r.CacheRead +
		float64(t.CacheWrite)*r.CacheWrite) / 1e6
}

// sortedKeys keeps family matching and the most-expensive fallback
// deterministic: Go map iteration order is randomised.
func sortedKeys(p Pricing) []string {
	ks := make([]string, 0, len(p))
	for k := range p {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func mostExpensive(p Pricing) Rate {
	var best Rate
	bestTotal := -1.0
	for _, k := range sortedKeys(p) {
		r := p[k]
		// Rank on the sum of all four terms, not Output alone: budget.pricing
		// is operator-configurable, so a cache-heavy override with a modest
		// output rate would otherwise win the comparison and price an unknown
		// model below the true worst case — weakening the deliberate
		// over-estimate documented on RateFor.
		total := r.Input + r.Output + r.CacheRead + r.CacheWrite
		if total > bestTotal {
			best, bestTotal = r, total
		}
	}
	return best
}
