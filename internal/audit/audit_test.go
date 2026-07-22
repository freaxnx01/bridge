package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogger_LogWritesOneValidJSONLinePerCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Log(Entry{
		Time:    time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
		Forge:   "github",
		Owner:   "freaxnx01",
		Repo:    "bridge",
		Tool:    "close_issue",
		Confirm: true,
		Outcome: "success",
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), string(data))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if got["forge"] != "github" || got["tool"] != "close_issue" || got["outcome"] != "success" || got["confirm"] != true {
		t.Errorf("entry: %+v", got)
	}
}

func TestLogger_AppendsAcrossMultipleOpensOfSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first.Log(Entry{Tool: "close_issue", Outcome: "success"})

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second.Log(Entry{Tool: "update_issue", Outcome: "error"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines across two opens, got %d: %q", len(lines), string(data))
	}
}

func TestOpen_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.jsonl")

	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("want audit file created, got %v", err)
	}
}
