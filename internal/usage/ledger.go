package usage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

// Run is one pipeline run bridge dispatched, priced at dispatch time with the
// calibrated mean. bridge is the only thing that applies the ai-implement
// label on a timer, so its own record of what it dispatched is the run
// population — no forge round-trip is needed to enumerate them.
type Run struct {
	At     time.Time `json:"at"`
	Repo   string    `json:"repo"`
	Issue  int       `json:"issue"`
	EstUSD float64   `json:"est_usd"`
}

// Ledger is the append-only local record of dispatched runs.
type Ledger struct {
	Runs []Run `json:"runs"`
}

// LoadLedger reads the ledger. A missing file is the first-run case, not an
// error — the same contract as dispatch.ReadState.
func LoadLedger(path string) (Ledger, error) {
	var l Ledger
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Ledger{}, nil
	}
	if err != nil {
		return Ledger{}, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return Ledger{}, err
	}
	return l, nil
}

// WriteLedger persists the ledger atomically.
func WriteLedger(path string, l Ledger) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

// Append records one dispatched run.
func (l *Ledger) Append(r Run) { l.Runs = append(l.Runs, r) }

// SumSince totals the estimated cost of runs dispatched at or after t.
func (l Ledger) SumSince(t time.Time) float64 {
	total := 0.0
	for _, r := range l.Runs {
		if r.At.Before(t) {
			continue
		}
		total += r.EstUSD
	}
	return total
}

// Prune drops runs older than before, keeping the file bounded. Only the
// trailing quota window is ever summed, so older entries have no readers.
func (l *Ledger) Prune(before time.Time) {
	kept := l.Runs[:0]
	for _, r := range l.Runs {
		if r.At.Before(before) {
			continue
		}
		kept = append(kept, r)
	}
	l.Runs = kept
}
