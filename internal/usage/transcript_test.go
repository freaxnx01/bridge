package usage

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// writeTranscript creates a .jsonl fixture and back-dates its mtime, so the
// mtime prefilter can be exercised without sleeping.
func writeTranscript(t *testing.T, dir, name, body string, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func row(ts, model string, in, out, cr, cw int) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"model":"` + model +
		`","usage":{"input_tokens":` + strconv.Itoa(in) + `,"output_tokens":` + strconv.Itoa(out) +
		`,"cache_read_input_tokens":` + strconv.Itoa(cr) +
		`,"cache_creation_input_tokens":` + strconv.Itoa(cw) + `}}}`
}

func TestScanTranscriptsReadsPricedTurns(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	writeTranscript(t, filepath.Join(root, "proj"), "a.jsonl",
		row(now.Add(-time.Hour).Format(time.RFC3339), "claude-opus-4-7", 10, 20, 30, 40)+"\n",
		now)

	turns, err := ScanTranscripts(context.Background(), root, now.Add(-5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(turns))
	}
	if turns[0].Model != "claude-opus-4-7" || turns[0].Tokens.CacheRead != 30 {
		t.Errorf("bad turn: %+v", turns[0])
	}
}

func TestScanTranscriptsSkipsFilesOlderThanTheWindow(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	// A file last written 10h ago cannot hold a row inside a 5h window.
	writeTranscript(t, filepath.Join(root, "proj"), "old.jsonl",
		row(now.Format(time.RFC3339), "claude-opus-4-7", 1, 1, 1, 1)+"\n",
		now.Add(-10*time.Hour))

	turns, err := ScanTranscripts(context.Background(), root, now.Add(-5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Errorf("mtime prefilter must skip the file, got %d turns", len(turns))
	}
}

func TestScanTranscriptsFiltersRowsAndToleratesGarbage(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	body := "not json at all\n" +
		`{"type":"user","timestamp":"` + now.Format(time.RFC3339) + `"}` + "\n" + // no usage
		`{"type":"assistant","message":{"model":"x","usage":{"input_tokens":5}}}` + "\n" + // no timestamp
		row(now.Add(-9*time.Hour).Format(time.RFC3339), "claude-opus-4-7", 7, 7, 7, 7) + "\n" + // too old
		row(now.Add(-time.Minute).Format(time.RFC3339), "claude-opus-4-7", 1, 2, 3, 4) + "\n"
	writeTranscript(t, filepath.Join(root, "proj"), "mixed.jsonl", body, now)

	turns, err := ScanTranscripts(context.Background(), root, now.Add(-5*time.Hour))
	if err != nil {
		t.Fatalf("a corrupt line must not fail the scan: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("want only the in-window priced row, got %d: %+v", len(turns), turns)
	}
	if turns[0].Tokens.Output != 2 {
		t.Errorf("wrong row survived: %+v", turns[0])
	}
}

func TestScanTranscriptsMissingRootIsNotAnError(t *testing.T) {
	turns, err := ScanTranscripts(context.Background(), filepath.Join(t.TempDir(), "nope"), time.Now())
	if err != nil {
		t.Fatalf("a missing transcript root is the fresh-install case: %v", err)
	}
	if len(turns) != 0 {
		t.Errorf("got %d turns", len(turns))
	}
}

func TestSumWindowIsHalfOpen(t *testing.T) {
	p := Pricing{"opus": {Output: 1e6}} // 1 USD per output token
	from := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	turns := []Turn{
		{At: from.Add(-time.Second), Model: "opus", Tokens: Tokens{Output: 1}}, // before
		{At: from, Model: "opus", Tokens: Tokens{Output: 1}},                   // inclusive
		{At: to.Add(-time.Second), Model: "opus", Tokens: Tokens{Output: 1}},   // inside
		{At: to, Model: "opus", Tokens: Tokens{Output: 1}},                     // exclusive
	}
	if got := SumWindow(turns, p, from, to); got != 2 {
		t.Errorf("want 2.0 (from inclusive, to exclusive), got %v", got)
	}
}
