package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadLedgerMissingFileIsEmpty(t *testing.T) {
	l, err := LoadLedger(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("first run must not error: %v", err)
	}
	if len(l.Runs) != 0 {
		t.Errorf("got %d runs", len(l.Runs))
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	now := time.Now().UTC().Truncate(time.Second)

	var l Ledger
	l.Append(Run{At: now, Repo: "bridge", Issue: 254, EstUSD: 2})
	if err := WriteLedger(path, l); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Issue != 254 || got.Runs[0].EstUSD != 2 {
		t.Errorf("round trip: %+v", got.Runs)
	}
	if !got.Runs[0].At.Equal(now) {
		t.Errorf("timestamp: %v != %v", got.Runs[0].At, now)
	}
}

func TestSumSinceIgnoresRunsOutsideTheWindow(t *testing.T) {
	now := time.Now().UTC()
	l := Ledger{Runs: []Run{
		{At: now.Add(-9 * time.Hour), EstUSD: 100},
		{At: now.Add(-2 * time.Hour), EstUSD: 2},
		{At: now.Add(-time.Minute), EstUSD: 3},
	}}
	if got := l.SumSince(now.Add(-5 * time.Hour)); got != 5 {
		t.Errorf("want 5, got %v", got)
	}
}

func TestPruneDropsOldRuns(t *testing.T) {
	now := time.Now().UTC()
	l := Ledger{Runs: []Run{
		{At: now.Add(-200 * time.Hour), EstUSD: 1},
		{At: now.Add(-time.Hour), EstUSD: 2},
	}}
	l.Prune(now.Add(-24 * time.Hour))
	if len(l.Runs) != 1 || l.Runs[0].EstUSD != 2 {
		t.Errorf("prune: %+v", l.Runs)
	}
}
