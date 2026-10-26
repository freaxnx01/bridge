// Package bot is the `bridge bot` screen state machine: per-chat State,
// Update(cfg, state, event) → (state, actions) and Render(state) → (text,
// keyboard). It is pure: every bit of I/O is an Action the runner executes,
// whose result comes back as an Event.
package bot

import (
	"regexp"
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

const (
	// PageSize is the number of repos per picker page (2 per row).
	PageSize = 8
	// IssuePageSize is the number of issues per issues page.
	IssuePageSize = 8
	// MaxStarred caps the ⭐ MRU block at the top of the picker (nav's Recent cap).
	MaxStarred = 5
	// MaxCallbackBytes is Telegram's callback_data limit.
	MaxCallbackBytes = 64
	// MaxFilterRunes bounds a filter typed in chat.
	MaxFilterRunes = 64
)

// Screen is the screen a chat is on.
type Screen int

const (
	ScreenPicker Screen = iota
	ScreenDash
	ScreenIssues
)

// Prompt is what the next free-text message means.
type Prompt int

const (
	PromptNone         Prompt = iota
	PromptFilter              // next text is the picker filter
	PromptNewRepoName         // next text is the new repo's name
	PromptNewRepoForge        // waiting for a forge × visibility button
	PromptNewWorktree         // next text is the new worktree's name
)

// ConfirmKind names what a ✅ button confirms.
type ConfirmKind string

const (
	ConfirmStop    ConfirmKind = "stop"
	ConfirmClone   ConfirmKind = "clone"
	ConfirmIssue   ConfirmKind = "issue"
	ConfirmNewRepo ConfirmKind = "repo"
)

// Confirm is a pending yes/no question about the item at Index (picker item,
// dashboard row, issue or repo choice, by Kind).
type Confirm struct {
	Kind  ConfirmKind
	Index int
}

// RowStatus is a dashboard row's git status; Err marks a failed read.
type RowStatus struct {
	Dirty navdata.Dirty
	Err   bool
}

// State is one chat's state. Treat it as a value: Update never mutates the
// slices or maps it receives, it replaces them.
type State struct {
	MessageID int // the chat's single edited message; 0 => none yet
	// Epoch is stamped into every button. It changes whenever the meaning of
	// a button index may change, so taps on an older keyboard are "outdated".
	Epoch int

	Screen      Screen
	Page        int
	Filter      string
	Forge       string // "" = All
	Local       []navdata.RepoEntry
	Remote      []navdata.RepoEntry
	MRU         []string
	ReposLoaded bool

	Repo         core.Repo
	Rows         []navdata.DashRow // nil => loading
	Status       map[string]RowStatus
	Issues       []forge.Issue
	IssuesLoaded bool
	IssuePage    int

	Prompt      Prompt
	NewRepoName string
	Confirm     *Confirm
	Busy        string          // non-empty while a slow action runs; taps are refused
	Fetching    map[string]bool // repo paths whose git fetch is in flight
	Notice      string          // one-line result or "⚠ …" error
}

// PickerItem is one picker button: an entry and whether it is in the ⭐ block.
type PickerItem struct {
	Entry navdata.RepoEntry
	Star  bool
}

// PickerItems is the full picker list (all pages): up to MaxStarred MRU repos
// first (⭐, most recent first), then everything else alphabetically by repo
// name. Button indexes ("r:<i>") index into this slice.
func (s State) PickerItems() []PickerItem {
	list := navdata.PickerList(s.Local, s.Remote, s.Forge, s.Filter)
	rank := make(map[string]int, len(s.MRU))
	for i, p := range s.MRU {
		if _, seen := rank[p]; !seen {
			rank[p] = i
		}
	}
	var starred, rest []PickerItem
	for _, e := range list {
		if e.Remote == nil {
			if _, ok := rank[e.Repo.Path]; ok {
				starred = append(starred, PickerItem{Entry: e, Star: true})
				continue
			}
		}
		rest = append(rest, PickerItem{Entry: e})
	}
	sort.SliceStable(starred, func(i, j int) bool {
		return rank[starred[i].Entry.Repo.Path] < rank[starred[j].Entry.Repo.Path]
	})
	if len(starred) > MaxStarred {
		for _, it := range starred[MaxStarred:] {
			it.Star = false
			rest = append(rest, it)
		}
		starred = starred[:MaxStarred]
	}
	sort.SliceStable(rest, func(i, j int) bool { return itemSortKey(rest[i]) < itemSortKey(rest[j]) })
	return append(starred, rest...)
}

func itemSortKey(it PickerItem) string {
	_, _, _, name := navdata.Parts(it.Entry)
	return strings.ToLower(name) + "\x00" + navdata.SortKey(it.Entry.Label)
}

// nameRE is the allowed shape of a repo/worktree name typed in chat.
// Compiled once; *regexp.Regexp is immutable and safe for concurrent use.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)

// ValidName reports whether s may be used as a new repo or worktree name:
// ^[A-Za-z0-9._-]{1,40}$ and not made only of dots ("." / ".." would resolve
// to the repo itself or its parent).
func ValidName(s string) bool {
	return nameRE.MatchString(s) && strings.Trim(s, ".") != ""
}

func pageCount(n, size int) int {
	if n <= 0 {
		return 1
	}
	return (n + size - 1) / size
}

func clamp(v, lo, hi int) int {
	if hi < lo || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func hasForgeIDs(r core.Repo) bool { return r.Forge != "" && r.Owner != "" && r.Name != "" }
