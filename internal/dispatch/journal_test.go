package dispatch

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func rec(at time.Time, outcome string) JournalRecord {
	return JournalRecord{At: at, Trigger: TriggerTimer, Mode: ModeDryRun, Outcome: outcome, LanesInWindow: []string{"default"}}
}

func TestAppendJournal_RoundTrip_ReadsBackWithVersion(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	r := rec(at, OutcomeDecided)
	r.Decisions = []JournalDecision{{Owner: "freaxnx01", Repo: "bridge", Issue: 41, Title: "t", Kind: KindDispatch, Lane: "default", Decision: DecisionWouldDispatch}}
	if err := AppendJournal(dir, r); err != nil {
		t.Fatal(err)
	}

	got, err := ReadJournal(dir, at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].V != JournalVersion || got[0].Decisions[0].Issue != 41 || !got[0].At.Equal(at) {
		t.Fatalf("got %+v", got)
	}
}

func TestAppendJournal_DayFile_NamedByUTCDate(t *testing.T) {
	dir := t.TempDir()
	zurich := time.FixedZone("CET", 3600)
	at := time.Date(2026, 11, 12, 0, 30, 0, 0, zurich) // 2026-11-11T23:30Z

	if err := AppendJournal(dir, rec(at, OutcomePaused)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-11.jsonl")); err != nil {
		t.Fatalf("want UTC day file: %v", err)
	}
}

func TestAppendJournal_Prune_RemovesOnlyFilesPastRetention(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2026-10-11.jsonl", "2026-10-12.jsonl", "notes.txt", "garbage.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) // cutoff day 2026-10-12

	if err := AppendJournal(dir, rec(at, OutcomeDecided)); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]bool{
		"2026-10-11.jsonl": false, "2026-10-12.jsonl": true, "notes.txt": true,
		"garbage.jsonl": true, "2026-11-11.jsonl": true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if exists := err == nil; exists != want {
			t.Errorf("%s exists=%t, want %t", name, exists, want)
		}
	}
}

func TestReadJournal_PartialTrailingLine_Skipped(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	if err := AppendJournal(dir, rec(at, OutcomeDecided)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "2026-11-11.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"at":"2026-11-11T02:30`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ReadJournal(dir, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d records, err %v", len(got), err)
	}
}

func TestReadJournal_Since_FiltersRecordsAndOlderFiles(t *testing.T) {
	dir := t.TempDir()
	d1 := time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 11, 11, 0, 30, 0, 0, time.UTC)
	// d3 lands in the day file before d2 on purpose: a manual tick and a timer
	// tick can finish out of order, so the reader sorts by At, not by position.
	for _, at := range []time.Time{d3, d1, d2} {
		if err := AppendJournal(dir, rec(at, OutcomeDecided)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ReadJournal(dir, d2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].At.Equal(d2) || !got[1].At.Equal(d3) {
		t.Fatalf("want [d2 d3] sorted by At, got %+v", got)
	}

	all, err := ReadJournal(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || !all[0].At.Equal(d1) {
		t.Fatalf("want 3 records, oldest (d1) first across files, got %+v", all)
	}
}

func TestReadJournal_MissingDir_NoRecordsNoError(t *testing.T) {
	got, err := ReadJournal(filepath.Join(t.TempDir(), "absent"), time.Time{})
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestAppendJournal_Concurrent_AllRecordsIntact(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := AppendJournal(dir, rec(at.Add(time.Duration(i)*time.Second), OutcomeDecided)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	got, err := ReadJournal(dir, time.Time{})
	if err != nil || len(got) != 20 {
		t.Fatalf("got %d records, err %v", len(got), err)
	}
}
