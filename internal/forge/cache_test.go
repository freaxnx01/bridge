package forge

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "issues.json")
	in := IssueCache{
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
		Issues:    []Issue{{Forge: "github", Repo: "f/x", Number: 1, Title: "t"}},
	}
	if err := WriteIssueCache(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadIssueCache(p)
	if err != nil {
		t.Fatal(err)
	}
	if !out.UpdatedAt.Equal(in.UpdatedAt) {
		t.Errorf("ts mismatch: %v vs %v", out.UpdatedAt, in.UpdatedAt)
	}
	if len(out.Issues) != 1 || out.Issues[0].Title != "t" {
		t.Errorf("payload mismatch: %+v", out)
	}
}

func TestCacheStale(t *testing.T) {
	fresh := IssueCache{UpdatedAt: time.Now().Add(-5 * time.Minute)}
	stale := IssueCache{UpdatedAt: time.Now().Add(-30 * time.Minute)}
	if fresh.IsStale(10 * time.Minute) {
		t.Error("fresh should not be stale")
	}
	if !stale.IsStale(10 * time.Minute) {
		t.Error("stale should be stale")
	}
}

func TestReadCacheMissingIsEmpty(t *testing.T) {
	c, err := ReadIssueCache(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Issues) != 0 {
		t.Errorf("want empty, got %+v", c)
	}
}

func TestCacheJSONShape(t *testing.T) {
	in := IssueCache{
		UpdatedAt: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		Issues:    []Issue{{Forge: "github", Repo: "a/b", Number: 1, Title: "t"}},
	}
	b, _ := json.Marshal(in)
	s := string(b)
	if !contains(s, `"updated_at"`) || !contains(s, `"issues"`) {
		t.Errorf("shape: %s", s)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
