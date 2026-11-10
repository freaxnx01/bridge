package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

func DefaultConfig() Config {
	return Config{
		Limits: Limits{
			GlobalOpenPRs:         3,
			PerRepo:               1,
			MaxDispatchesPerNight: 5,
		},
		Schedule: Schedule{Windows: []Window{
			{Span: Span{From: "18:00", To: "07:00"}, BudgetRung: false},
			{Span: Span{From: "07:00", To: "18:00"}, BudgetRung: true},
		}},
		Budget: Budget{
			WindowHours:     5,
			WindowBudgetUSD: 12.0,
			DaytimeCap:      0.80,
			MeanRunCostUSD:  2.0,
		},
	}
}

// LoadConfig reads path over the defaults. A missing file is not an error —
// the zero-config case must work. Unmarshalling into an already-populated
// struct is what keeps unset keys at their defaults rather than zero.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return DefaultConfig(), err
	}
	return c, nil
}

// LimitFor returns the per-repo concurrency limit for a bare repo name.
func (c Config) LimitFor(repo string) int {
	if n, ok := c.Limits.Overrides[repo]; ok {
		return n
	}
	return c.Limits.PerRepo
}

// DefaultLane is where a repo lands when no configured lane matches. It
// reproduces the pre-lane behaviour exactly: the top-level schedule and
// limits, and the single ai-implement label.
func DefaultLane() Lane {
	return Lane{Name: "default", Repos: []string{"*"}}
}

// EffectiveLabels returns the labels a dispatch in this lane applies, in one
// AddLabels call. An autonomous lane carries the ai-merge gate label alongside
// the trigger; every other lane applies the trigger alone, which is what
// bridge did before lanes existed. An explicit list always wins — applying a
// gate label to a repo that has not wired the matching workflow input is inert
// at best, so bridge never guesses one.
func (l Lane) EffectiveLabels() []string {
	if len(l.Labels) > 0 {
		return l.Labels
	}
	if l.Autonomous {
		return []string{LabelAIImplement, LabelAIReviewAIMerge}
	}
	return []string{LabelAIImplement}
}
