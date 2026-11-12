package dispatch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// JournalVersion stamps every record so a reader can tell shapes apart.
	JournalVersion = 1
	// JournalRetentionDays bounds the journal: day files older than this are
	// pruned after each append. A const, not config — nothing tunes it.
	JournalRetentionDays = 30

	// journalDayLayout names a day file and is also the record's UTC day.
	journalDayLayout = "2006-01-02"
	journalDaySuffix = ".jsonl"
	// journalLineLimit bounds one record's line. A tick with many decisions is
	// still far below it; the scanner's own default (64 KiB) is not.
	journalLineLimit = 1 << 20
)

// Trigger values: what started the tick.
const (
	TriggerTimer  = "timer"
	TriggerManual = "manual"
)

// Mode values: whether the tick could apply anything.
const (
	ModeLive   = "live"
	ModeDryRun = "dry-run"
)

// Outcome values: how the tick as a whole ended.
const (
	OutcomeDecided       = "decided"
	OutcomeNoCandidates  = "no_candidates"
	OutcomePaused        = "paused"
	OutcomeOutsideWindow = "outside_window"
	OutcomeError         = "error"
)

// Decision values: what became of one candidate. NotApplied is a live decision
// that was meant to dispatch and never got booked — the tick failed first.
const (
	DecisionDispatched    = "dispatched"
	DecisionWouldDispatch = "would_dispatch"
	DecisionSkipped       = "skipped"
	DecisionNotApplied    = "not_applied"
)

// Kind values: which pass a decision came from.
const (
	KindDispatch = "dispatch"
	KindRetry    = "retry"
)

// JournalRecord is one dispatch tick. The json shape is a contract with the
// cockpit digest: add fields, never rename or repurpose one.
type JournalRecord struct {
	V             int               `json:"v"`
	At            time.Time         `json:"at"`
	Trigger       string            `json:"trigger"`
	Mode          string            `json:"mode"`
	RetryOnly     bool              `json:"retry_only,omitempty"`
	Outcome       string            `json:"outcome"`
	Error         string            `json:"error,omitempty"`
	LanesInWindow []string          `json:"lanes_in_window"`
	Repos         []string          `json:"repos,omitempty"`
	Budget        *JournalBudget    `json:"budget,omitempty"`
	Decisions     []JournalDecision `json:"decisions,omitempty"`
}

// JournalBudget is the budget rung's reading, present only when the rung is on.
type JournalBudget struct {
	UsedUSD  float64 `json:"used_usd"`
	LimitUSD float64 `json:"limit_usd"`
	Known    bool    `json:"known"`
}

// JournalDecision is one candidate's outcome within a tick.
type JournalDecision struct {
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Issue       int    `json:"issue"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	RetryReason string `json:"retry_reason,omitempty"`
	Lane        string `json:"lane"`
	LaneReason  string `json:"lane_reason,omitempty"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason,omitempty"`
}

// AppendJournal writes rec as one line to dir's day file for rec.At (UTC) and
// then prunes day files past the retention. rec.V is forced to JournalVersion.
//
// The write is a single O_APPEND Write of a whole line, which is what lets a
// concurrent reader — or a second dispatcher — never see a torn record.
func AppendJournal(dir string, rec JournalRecord) error {
	rec.V = JournalVersion
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create journal dir %s: %w", dir, err)
	}
	if err := appendLine(dayFile(dir, rec.At), append(line, '\n')); err != nil {
		return err
	}
	return pruneJournal(dir, rec.At)
}

// appendLine writes one line to path, creating it if needed.
func appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open journal %s: %w", path, err)
	}
	_, writeErr := f.Write(line)
	if writeErr != nil {
		writeErr = fmt.Errorf("write journal %s: %w", path, writeErr)
	}
	closeErr := f.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close journal %s: %w", path, closeErr)
	}
	return errors.Join(writeErr, closeErr)
}

// dayFile is the file a record at `at` belongs in, named by its UTC day.
func dayFile(dir string, at time.Time) string {
	return filepath.Join(dir, at.UTC().Format(journalDayLayout)+journalDaySuffix)
}

// pruneJournal deletes day files older than the retention. A name that is not
// a day file is left alone: the journal dir is not the pruner's to own.
func pruneJournal(dir string, at time.Time) error {
	cutoff := at.UTC().Truncate(24*time.Hour).AddDate(0, 0, -JournalRetentionDays)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read journal dir %s: %w", dir, err)
	}
	var errs []error
	for _, e := range entries {
		day, ok := dayOf(e.Name())
		if !ok || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			errs = append(errs, fmt.Errorf("prune journal %s: %w", e.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// dayOf parses a day file's name. The bool reports whether it is one at all.
func dayOf(name string) (time.Time, bool) {
	if !strings.HasSuffix(name, journalDaySuffix) {
		return time.Time{}, false
	}
	day, err := time.Parse(journalDayLayout, strings.TrimSuffix(name, journalDaySuffix))
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

// ReadJournal returns the records at or after since, oldest first. A missing
// dir is no records. Lines that do not decode — a partial last line read while
// a tick is mid-write — are skipped. It takes no lock and blocks no tick.
func ReadJournal(dir string, since time.Time) ([]JournalRecord, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read journal dir %s: %w", dir, err)
	}
	sinceDay := since.UTC().Truncate(24 * time.Hour)

	var out []JournalRecord
	for _, e := range entries {
		day, ok := dayOf(e.Name())
		if !ok || day.Before(sinceDay) {
			continue
		}
		recs, err := readDayFile(filepath.Join(dir, e.Name()), since)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// readDayFile decodes one day file's records at or after since.
func readDayFile(path string, since time.Time) ([]JournalRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // best-effort close of a read-only file

	var out []JournalRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), journalLineLimit)
	for scanner.Scan() {
		var rec JournalRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if !rec.At.Before(since) {
			out = append(out, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan journal %s: %w", path, err)
	}
	return out, nil
}
