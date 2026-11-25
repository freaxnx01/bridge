package cockpit

import (
	"container/list"
	"context"
	"sync"

	"github.com/freaxnx01/bridge/internal/forge"
)

// DiffFile is one file of a pull request's diff. PatchMissing says the forge
// reported no patch at all — a binary file, or one too large for it to diff —
// which an empty Patch alone cannot be told apart from an empty file.
type DiffFile struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Patch        string `json:"patch"`
	PatchMissing bool   `json:"patchMissing"`
}

// Diff is one pull request's read-only per-file diff at HeadSHA. Truncated
// says the PR changes more files than the first page carries.
type Diff struct {
	PR        int        `json:"pr"`
	HeadSHA   string     `json:"headSha"`
	Files     []DiffFile `json:"files"`
	Truncated bool       `json:"truncated"`
}

// NewDiff turns a forge file list into one pull request's diff.
func NewDiff(pr int, headSHA string, files []forge.PRFile, truncated bool) Diff {
	out := make([]DiffFile, 0, len(files))
	for _, f := range files {
		out = append(out, DiffFile{
			Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions,
			Patch: f.Patch, PatchMissing: f.Patch == "",
		})
	}
	return Diff{PR: pr, HeadSHA: headSHA, Files: out, Truncated: truncated}
}

// DiffKey identifies one cached diff. The head SHA is part of it, so a push
// cannot be served a diff of the code it replaced: the new head simply misses.
type DiffKey struct {
	Repo    string // "owner/name"
	PR      int
	HeadSHA string
}

// DiffCache holds the diffs read so far, newest use first, dropping the least
// recently used one past its capacity. Patches are the largest thing the
// cockpit keeps in memory, and a diff nobody looked at twice is not worth it.
type DiffCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[DiffKey]*list.Element
	order    *list.List // front = most recently used; values are *diffEntry
}

type diffEntry struct {
	key  DiffKey
	diff Diff
}

// NewDiffCache returns a cache holding at most capacity diffs. A capacity of
// zero or less caches nothing.
func NewDiffCache(capacity int) *DiffCache {
	return &DiffCache{
		capacity: capacity,
		entries:  map[DiffKey]*list.Element{},
		order:    list.New(),
	}
}

// Get returns the diff for k, calling load once for a key it does not hold. A
// failed load is not cached: the next request retries rather than serving the
// error for as long as the entry would live.
//
// load runs without the lock held, so two requests racing on the same cold key
// may both read the forge; the result is the same either way, and the
// alternative is serializing every diff read behind one mutex.
func (c *DiffCache) Get(ctx context.Context, k DiffKey, load func(ctx context.Context) (Diff, error)) (Diff, error) {
	if d, ok := c.lookup(k); ok {
		return d, nil
	}
	d, err := load(ctx)
	if err != nil {
		return Diff{}, err
	}
	c.store(k, d)
	return d, nil
}

// lookup returns the cached diff for k and marks it most recently used.
func (c *DiffCache) lookup(k DiffKey) (Diff, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[k]
	if !ok {
		return Diff{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*diffEntry).diff, true
}

// store adds the diff as the most recently used entry, evicting the least
// recently used one when the cache is full.
func (c *DiffCache) store(k DiffKey, d Diff) {
	if c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[k]; ok {
		el.Value.(*diffEntry).diff = d
		c.order.MoveToFront(el)
		return
	}
	c.entries[k] = c.order.PushFront(&diffEntry{key: k, diff: d})
	if c.order.Len() <= c.capacity {
		return
	}
	oldest := c.order.Back()
	c.order.Remove(oldest)
	delete(c.entries, oldest.Value.(*diffEntry).key)
}
