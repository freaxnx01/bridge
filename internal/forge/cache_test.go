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

func TestWriteIssueCache_StripsBodyAndLeavesCallerSliceIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issues.json")
	issues := []Issue{
		{Number: 1, Title: "open bug", Body: "a long private issue body"},
		{Number: 2, Title: "another", Body: "more prose"},
	}

	if err := WriteIssueCache(path, IssueCache{UpdatedAt: time.Now(), Issues: issues}); err != nil {
		t.Fatal(err)
	}

	// The cache is an on-disk outward boundary: it is read back only for titles,
	// labels and counts, so persisting bodies would write the full text of every
	// open issue — private repos included — to an unencrypted file.
	got, err := ReadIssueCache(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range got.Issues {
		if i.Body != "" {
			t.Errorf("issue #%d: cached Body = %q, want empty", i.Number, i.Body)
		}
	}
	if len(got.Issues) != 2 || got.Issues[0].Title != "open bug" {
		t.Errorf("stripping must not disturb the rest: %+v", got.Issues)
	}

	// Stripping must not reach back into the caller's slice — dispatch reads
	// Body off the same shape and would see every issue as empty.
	if issues[0].Body != "a long private issue body" {
		t.Errorf("caller's slice was mutated: Body = %q", issues[0].Body)
	}
}
