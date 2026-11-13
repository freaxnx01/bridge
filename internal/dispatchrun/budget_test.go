package dispatchrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/usage"
)

func TestMeasureWindowUSD_InteractiveAndPipeline_AreBothCounted(t *testing.T) {
	projects := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "usage.json")
	now := time.Now().UTC()

	// One interactive turn: 1M output tokens of opus at 75 USD/Mtok.
	line := `{"type":"assistant","timestamp":"` + now.Add(-time.Hour).Format(time.RFC3339) +
		`","message":{"model":"claude-opus-4-7","usage":{"input_tokens":0,"output_tokens":1000000,` +
		`"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
	dir := filepath.Join(projects, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// One dispatched run in the ledger.
	var l usage.Ledger
	l.Append(usage.Run{At: now.Add(-time.Minute), Repo: "bridge", Issue: 254, EstUSD: 2})
	if err := usage.WriteLedger(ledgerPath, l); err != nil {
		t.Fatal(err)
	}

	got, ok := MeasureWindowUSD(context.Background(), dispatch.DefaultConfig(),
		projects, ledgerPath, now.Add(-5*time.Hour), now)

	if !ok {
		t.Fatal("measurement should be known")
	}
	if diff := got - 77.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("want 75 interactive + 2 pipeline = 77, got %v", got)
	}
}

func TestMeasureWindowUSD_MissingSources_AreZeroNotUnknown(t *testing.T) {
	now := time.Now()

	got, ok := MeasureWindowUSD(context.Background(), dispatch.DefaultConfig(),
		filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "absent.json"),
		now.Add(-5*time.Hour), now)

	if !ok || got != 0 {
		t.Errorf("a fresh install has measured zero usage, not unknown: got=%v ok=%v", got, ok)
	}
}

func TestMeasureWindowUSD_UnreadableLedger_IsUnknown(t *testing.T) {
	// Corrupt ledger: readable file, invalid JSON.
	ledgerPath := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(ledgerPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	_, ok := MeasureWindowUSD(context.Background(), dispatch.DefaultConfig(),
		filepath.Join(t.TempDir(), "absent"), ledgerPath, now.Add(-5*time.Hour), now)

	if ok {
		t.Error("a corrupt ledger must report unknown so the rung fails closed")
	}
}
