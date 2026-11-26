package main

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// diffFiles is what the diff path reads off the forge: the PR's files, patches
// included.
func diffFiles() []forge.PRFile {
	return []forge.PRFile{
		{Path: "a.go", Status: "modified", Additions: 3, Deletions: 1, Patch: "@@ -1 +1 @@\n+x"},
		{Path: "img.png", Status: "added"},
	}
}

func TestInboxDiff_ShownCard_ReadsThePRFiles(t *testing.T) {
	m := greenMerger()
	m.files = diffFiles()
	cards := shownCard(9)
	cards.card.Merge.FilesTruncated = true
	var gotForge, gotOwner string
	resolve := func(f, o string) forge.Client {
		gotForge, gotOwner = f, o
		return mergeClient{fakeMerger: m}
	}

	d, err := inboxDiff(context.Background(), cards, mergeRepos, resolve,
		cockpit.NewDiffCache(32), "o", "a", 5)
	if err != nil {
		t.Fatal(err)
	}
	if gotForge != "github" || gotOwner != "o" {
		t.Errorf("resolved %s/%s, want the repo owner's client", gotForge, gotOwner)
	}
	if !m.did("get o/a#9") || !m.did("files o/a#9") {
		t.Errorf("calls = %q, want the head verified before the files are read", m.calls)
	}
	if d.PR != 9 || d.HeadSHA != mergeHead || !d.Truncated {
		t.Errorf("diff = %+v", d)
	}
	if len(d.Files) != 2 || d.Files[0].Patch != "@@ -1 +1 @@\n+x" || !d.Files[1].PatchMissing {
		t.Errorf("files = %+v", d.Files)
	}
}

func TestDiff_NotOnInbox_409NoForgeCall(t *testing.T) {
	humanCard := shownCard(9)
	humanCard.card.Kind = "human"
	noEvidence := shownCard(9)
	noEvidence.card.Merge = nil
	cases := []struct {
		name  string
		cards *cardCollector
	}{
		{"the issue has no card at all", &cardCollector{}},
		{"the card is not a merge card", humanCard},
		{"the card carries no PR evidence", noEvidence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := greenMerger()
			resolved := false
			resolve := func(string, string) forge.Client {
				resolved = true
				return mergeClient{fakeMerger: m}
			}

			_, err := inboxDiff(context.Background(), tc.cards, mergeRepos, resolve,
				cockpit.NewDiffCache(32), "o", "a", 5)
			if !errors.Is(err, cockpit.ErrNotCandidate) {
				t.Fatalf("err = %v, want ErrNotCandidate", err)
			}
			if resolved {
				t.Error("resolved a client for an issue that is not on the inbox")
			}
		})
	}
}

func TestInboxDiff_CachesPerHeadSHA(t *testing.T) {
	m := greenMerger()
	m.files = diffFiles()
	cards := shownCard(9)
	cache := cockpit.NewDiffCache(32)
	resolve := func(string, string) forge.Client { return mergeClient{fakeMerger: m} }
	reads := func() int {
		n := 0
		for _, c := range m.calls {
			if c == "files o/a#9" {
				n++
			}
		}
		return n
	}

	for range 2 {
		if _, err := inboxDiff(context.Background(), cards, mergeRepos, resolve, cache, "o", "a", 5); err != nil {
			t.Fatal(err)
		}
	}
	if reads() != 1 {
		t.Errorf("file reads = %d, want the second request served from the cache", reads())
	}

	// A push the collector has already seen: the card and the PR are both at
	// the new head, so the diff of that head is read fresh.
	cards.card.Merge.HeadSHA = mergeOld
	m.pr.HeadSHA = mergeOld
	if _, err := inboxDiff(context.Background(), cards, mergeRepos, resolve, cache, "o", "a", 5); err != nil {
		t.Fatal(err)
	}
	if reads() != 2 {
		t.Errorf("file reads = %d, want a pushed head to be re-read", reads())
	}
}

// The file listing answers for the PR's current head, so a head that moved
// since the card was collected would serve the operator code the card's review
// verdict was never given on — labelled, and cached, under the old SHA.
func TestInboxDiff_HeadMovedSinceTheCard_RefusedBeforeTheFiles(t *testing.T) {
	m := greenMerger()
	m.files = diffFiles()
	m.pr.HeadSHA = "2222222222222222222222222222222222222222"
	cards := shownCard(9)
	resolve := func(string, string) forge.Client { return mergeClient{fakeMerger: m} }

	_, err := inboxDiff(context.Background(), cards, mergeRepos, resolve,
		cockpit.NewDiffCache(32), "o", "a", 5)
	if !errors.Is(err, cockpit.ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}
	if m.didAny("files ") {
		t.Errorf("calls = %q, a moved head is not worth the file read", m.calls)
	}
}

// The head and the files are two reads: a push landing between them would
// serve, and cache, the new commit's code under the card's older SHA. The head
// is checked again after the files, and the refused read is not cached.
func TestInboxDiff_PushDuringTheFileRead_RefusedAndNotCached(t *testing.T) {
	m := greenMerger()
	m.files = diffFiles()
	m.onFiles = func(f *fakeMerger) { f.pr.HeadSHA = "2222222222222222222222222222222222222222" }
	cache := cockpit.NewDiffCache(32)
	resolve := func(string, string) forge.Client { return mergeClient{fakeMerger: m} }

	_, err := inboxDiff(context.Background(), shownCard(9), mergeRepos, resolve, cache, "o", "a", 5)
	if !errors.Is(err, cockpit.ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}

	m.onFiles = nil
	m.pr.HeadSHA = mergeHead
	m.calls = nil
	if _, err := inboxDiff(context.Background(), shownCard(9), mergeRepos, resolve, cache, "o", "a", 5); err != nil {
		t.Fatal(err)
	}
	if !m.did("files o/a#9") {
		t.Errorf("calls = %q, want the refused read not served from the cache", m.calls)
	}
}

// The repo-level refusals are the merge path's own sentinels, which the handler
// maps to the card's 409/422 — not to a 502 "forge call failed".
func TestInboxDiff_RepoAndClientFailures(t *testing.T) {
	cases := []struct {
		name   string
		client forge.Client
		repos  func() ([]core.Repo, error)
		want   error
	}{
		{"repo no longer wired", mergeClient{fakeMerger: greenMerger()},
			func() ([]core.Repo, error) { return nil, nil }, api.ErrUnknownRepo},
		{"repos listing fails", mergeClient{fakeMerger: greenMerger()},
			func() ([]core.Repo, error) { return nil, errors.New("boom") }, nil},
		{"no client for the owner", nil, mergeRepos, api.ErrNoToken},
		{"client cannot read PRs", baseClient{}, mergeRepos, api.ErrMergeUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(string, string) forge.Client { return tc.client }
			_, err := inboxDiff(context.Background(), shownCard(9), tc.repos, resolve,
				cockpit.NewDiffCache(32), "o", "a", 5)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestInboxDiff_ForgeFails_NotCached(t *testing.T) {
	m := greenMerger()
	m.filesErr = errors.New("502 bad gateway")
	cards := shownCard(9)
	cache := cockpit.NewDiffCache(32)
	resolve := func(string, string) forge.Client { return mergeClient{fakeMerger: m} }

	if _, err := inboxDiff(context.Background(), cards, mergeRepos, resolve, cache, "o", "a", 5); err == nil {
		t.Fatal("want the forge error")
	}
	m.filesErr = nil
	m.files = diffFiles()
	d, err := inboxDiff(context.Background(), cards, mergeRepos, resolve, cache, "o", "a", 5)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if len(d.Files) != 2 {
		t.Errorf("files = %+v, want the retry to read the forge again", d.Files)
	}
}
