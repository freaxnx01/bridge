package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

// ReadState loads the dispatcher's local state. A missing file is the
// first-run case, not an error.
func ReadState(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

func WriteState(path string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

// NightBudgetUsed returns how much of the nightly ceiling this night has
// already spent. A "night" runs from one evening into the following morning,
// so a 02:00 retry tick belongs to the previous calendar day's night — hence
// the 12:00 pivot rather than a date comparison.
func (s State) NightBudgetUsed(now time.Time) int {
	if s.NightStartedAt.IsZero() {
		return 0
	}
	if nightOf(now).Equal(nightOf(s.NightStartedAt)) {
		return s.DispatchedTonight
	}
	return 0
}

// nightOf maps an instant to the date its night began. Anything before noon
// belongs to the previous day's night.
func nightOf(t time.Time) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	if t.Hour() < 12 {
		d = d.AddDate(0, 0, -1)
	}
	return d
}
