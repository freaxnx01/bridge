package cockpit

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

var diffKey = DiffKey{Repo: "o/a", PR: 9, HeadSHA: mergeHead}

// counting returns a loader that answers with one file naming the call count,
// plus a pointer to that count.
func counting() (func(context.Context) (Diff, error), *int) {
	calls := 0
	return func(context.Context) (Diff, error) {
		calls++
		return Diff{PR: 9, Files: []DiffFile{{Path: "a.go", Additions: calls}}}, nil
	}, &calls
}

func TestDiffCache_HitDoesNotReload(t *testing.T) {
	load, calls := counting()
	c := NewDiffCache(32)

	first, err := c.Get(context.Background(), diffKey, load)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Get(context.Background(), diffKey, load)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("loads = %d, want 1", *calls)
	}
	if first.Files[0].Additions != second.Files[0].Additions {
		t.Errorf("second read differs: %+v vs %+v", first, second)
	}
}

// The collector reads a merge candidate's files to build its evidence and the
// open inbox drops the patches again; what it puts here is what the diff
// endpoint then serves without a forge call of its own.
func TestDiffCache_Put_ServesTheGetWithoutLoading(t *testing.T) {
	load, calls := counting()
	c := NewDiffCache(32)
	seeded := Diff{PR: 9, HeadSHA: mergeHead, Files: []DiffFile{{Path: "a.go", Patch: "@@ -1 +1 @@\n+x"}}}

	c.Put(diffKey, seeded)
	got, err := c.Get(context.Background(), diffKey, load)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("loads = %d, want the put diff served as is", *calls)
	}
	if len(got.Files) != 1 || got.Files[0].Patch != seeded.Files[0].Patch {
		t.Errorf("diff = %+v", got)
	}
}

func TestDiffCache_NewHeadSHA_Refetches(t *testing.T) {
	load, calls := counting()
	c := NewDiffCache(32)
	moved := diffKey
	moved.HeadSHA = mergeOld

	if _, err := c.Get(context.Background(), diffKey, load); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), moved, load); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Errorf("loads = %d, want the pushed head to miss the cache", *calls)
	}
}

func TestDiffCache_EvictsLeastRecent(t *testing.T) {
	load, calls := counting()
	c := NewDiffCache(2)
	a := diffKey
	b := DiffKey{Repo: "o/a", PR: 10, HeadSHA: mergeHead}
	d := DiffKey{Repo: "o/a", PR: 11, HeadSHA: mergeHead}

	for _, k := range []DiffKey{a, b, a, d} {
		if _, err := c.Get(context.Background(), k, load); err != nil {
			t.Fatal(err)
		}
	}
	if *calls != 3 {
		t.Fatalf("loads = %d, want 3 (a, b, d — a's second read a hit)", *calls)
	}
	// a was used after b, so b is the one that went.
	if _, err := c.Get(context.Background(), a, load); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Errorf("loads = %d, want a still cached", *calls)
	}
	if _, err := c.Get(context.Background(), b, load); err != nil {
		t.Fatal(err)
	}
	if *calls != 4 {
		t.Errorf("loads = %d, want b evicted and reloaded", *calls)
	}
}

func TestDiffCache_ErrorNotCached(t *testing.T) {
	calls := 0
	load := func(context.Context) (Diff, error) {
		calls++
		if calls == 1 {
			return Diff{}, errors.New("forge down")
		}
		return Diff{PR: 9}, nil
	}
	c := NewDiffCache(32)

	if _, err := c.Get(context.Background(), diffKey, load); err == nil {
		t.Fatal("first Get: want the loader's error")
	}
	got, err := c.Get(context.Background(), diffKey, load)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if calls != 2 || got.PR != 9 {
		t.Errorf("loads = %d, diff = %+v, want the failure not cached", calls, got)
	}
}

func TestNewDiff_MarksMissingPatches(t *testing.T) {
	files := []forge.PRFile{
		{Path: "a.go", Status: "modified", Additions: 1, Deletions: 2, Patch: "@@ -1 +1 @@\n+x"},
		{Path: "img.png", Status: "added"},
	}
	d := NewDiff(9, mergeHead, files, true)

	if d.PR != 9 || d.HeadSHA != mergeHead || !d.Truncated {
		t.Errorf("diff = %+v", d)
	}
	want := []DiffFile{
		{Path: "a.go", Status: "modified", Additions: 1, Deletions: 2, Patch: "@@ -1 +1 @@\n+x"},
		{Path: "img.png", Status: "added", PatchMissing: true},
	}
	if len(d.Files) != 2 || d.Files[0] != want[0] || d.Files[1] != want[1] {
		t.Errorf("files = %+v, want %+v", d.Files, want)
	}
}
