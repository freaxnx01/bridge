// Package navdata holds nav's data loading and mutations as plain,
// context-taking functions with no Bubble Tea dependency, so the `bridge nav`
// TUI and the `bridge bot` Telegram front end always show the same state.
package navdata

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// RepoEntry is one picker entry: a local repo, or a remote-only repo
// (Remote != nil) offered for clone-on-select.
type RepoEntry struct {
	Label  string         // e.g. "github/public/bridge"; remote entries carry a "↓ " prefix
	Repo   core.Repo      // zero for remote-only entries
	Remote *forge.RepoRef // non-nil => clone-on-select
}

// Entry lets RepoEntry satisfy Row.
func (e RepoEntry) Entry() RepoEntry { return e }

// Row is any picker row that wraps a RepoEntry: nav's repoRow (which adds
// issue counts) or RepoEntry itself. The generic list helpers below accept any
// Row so nav and the bot share one implementation.
type Row interface{ Entry() RepoEntry }

// RepoLabel is the picker label for a local repo.
func RepoLabel(r core.Repo) string {
	switch r.Forge {
	case "github":
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		return "github/" + vis + "/" + r.Name
	case "forgejo":
		return "forgejo/" + r.Name
	default:
		if r.Owner != "" {
			return r.Forge + "/" + r.Owner + "/" + r.Name
		}
		return r.Forge + "/" + r.Name
	}
}

// RemoteLabel is the picker label (without the "↓ " prefix) for a remote ref.
func RemoteLabel(r forge.RepoRef) string {
	if r.Forge == "github" {
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		return "github/" + vis + "/" + r.Name
	}
	if r.Owner != "" {
		return r.Forge + "/" + r.Owner + "/" + r.Name
	}
	return r.Forge + "/" + r.Name
}

// LocalRepos discovers the repos under roots, deduped by path (first root
// wins) and sorted by label. A root that fails discovery is skipped: nav's
// loaders are best effort.
func LocalRepos(ctx context.Context, roots []string) []RepoEntry {
	var out []RepoEntry
	seen := map[string]bool{}
	for _, root := range roots {
		if ctx.Err() != nil {
			break
		}
		repos, err := core.DiscoverRepos(root)
		if err != nil {
			continue
		}
		for _, r := range repos {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			out = append(out, RepoEntry{Label: RepoLabel(r), Repo: r})
		}
	}
	SortRows(out)
	return out
}

// RemoteEntries builds sorted, "↓ "-prefixed entries from forge refs.
func RemoteEntries(refs []forge.RepoRef) []RepoEntry {
	out := make([]RepoEntry, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		out = append(out, RepoEntry{Label: "↓ " + RemoteLabel(ref), Remote: &ref})
	}
	SortRows(out)
	return out
}

// RemoteRepos reads the remote-repo cache (remote.list). A missing cache is
// not an error (it yields no entries).
func RemoteRepos(cachePath string) ([]RepoEntry, error) {
	c, err := forge.ReadRepoCache(cachePath)
	if err != nil {
		return nil, fmt.Errorf("read remote cache %s: %w", cachePath, err)
	}
	return RemoteEntries(c.Repos), nil
}

// RecentPaths returns the MRU file's repo paths, most-recent-first. A read
// error yields an empty list (best effort, like nav).
func RecentPaths(path string) []string {
	paths, _ := core.LoadMRU(path)
	return paths
}

// SortKey is the case-insensitive sort key of a label, ignoring the remote
// "↓ " prefix so local and remote rows compare on the same key.
func SortKey(label string) string {
	return strings.ToLower(strings.TrimPrefix(label, "↓ "))
}

// SortRows orders rows by SortKey(label). Stable.
func SortRows[T Row](rows []T) {
	sort.SliceStable(rows, func(i, j int) bool {
		return SortKey(rows[i].Entry().Label) < SortKey(rows[j].Entry().Label)
	})
}

// Parts returns forge, owner, visibility and name of an entry, from its
// remote ref when present, otherwise its local repo.
func Parts(e RepoEntry) (forgeName, owner, vis, name string) {
	if e.Remote != nil {
		return e.Remote.Forge, e.Remote.Owner, e.Remote.Visibility, e.Remote.Name
	}
	return e.Repo.Forge, e.Repo.Owner, e.Repo.Visibility, e.Repo.Name
}

// Key is the case-insensitive forge+owner+name identity of an entry. The local
// owner comes from the on-disk path, the remote owner from the forge API, and
// their casing can differ (#124).
func Key(e RepoEntry) string {
	f, o, _, n := Parts(e)
	return strings.ToLower(f + "\x00" + o + "\x00" + n)
}

// DedupRemote drops remote rows whose repo is already cloned locally.
func DedupRemote[T Row](local, remote []T) []T {
	have := make(map[string]bool, len(local))
	for _, r := range local {
		have[Key(r.Entry())] = true
	}
	out := make([]T, 0, len(remote))
	for _, r := range remote {
		if have[Key(r.Entry())] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// OwnerQualifiedLabel returns e's label with the owner injected (github only:
// github/<vis>/<owner>/<name>); other forges already carry the owner. The
// remote "↓ " prefix is preserved.
func OwnerQualifiedLabel(e RepoEntry) string {
	f, owner, vis, name := Parts(e)
	if f != "github" {
		return e.Label
	}
	if vis == "" {
		vis = "-"
	}
	display := "github/" + vis + "/" + owner + "/" + name
	if e.Remote != nil {
		return "↓ " + display
	}
	return display
}

// DisambiguateOwners owner-qualifies the labels of rows that render
// identically but belong to different owners. relabel returns a copy of a row
// with a new label. Returns a new slice; rows is not mutated.
func DisambiguateOwners[T Row](rows []T, relabel func(T, string) T) []T {
	idxByLabel := map[string][]int{}
	ownersByLabel := map[string]map[string]bool{}
	for i, r := range rows {
		key := SortKey(r.Entry().Label)
		idxByLabel[key] = append(idxByLabel[key], i)
		if ownersByLabel[key] == nil {
			ownersByLabel[key] = map[string]bool{}
		}
		_, owner, _, _ := Parts(r.Entry())
		ownersByLabel[key][strings.ToLower(owner)] = true
	}
	out := append([]T{}, rows...)
	for key, idxs := range idxByLabel {
		if len(ownersByLabel[key]) < 2 {
			continue
		}
		for _, i := range idxs {
			out[i] = relabel(out[i], OwnerQualifiedLabel(out[i].Entry()))
		}
	}
	return out
}

// MatchesAllTerms reports whether every term is a substring of labelLower.
// Both must already be lower-cased. No terms is vacuously true.
func MatchesAllTerms(labelLower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(labelLower, t) {
			return false
		}
	}
	return true
}

// FilterRows keeps rows whose label contains every whitespace-separated term
// of q (case-insensitive AND). A blank q returns rows unchanged.
func FilterRows[T Row](rows []T, q string) []T {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	terms := strings.Fields(strings.ToLower(q))
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		if MatchesAllTerms(strings.ToLower(r.Entry().Label), terms) {
			out = append(out, r)
		}
	}
	return out
}

// MatchesForge reports whether e belongs to forgeKey; "" (All) matches all.
func MatchesForge(e RepoEntry, forgeKey string) bool {
	if forgeKey == "" {
		return true
	}
	f, _, _, _ := Parts(e)
	return f == forgeKey
}

// ForgeOpt is one forge-filter option: the forge key and its display label.
type ForgeOpt struct {
	Key, Label string
}

// ForgeOptions is the canonical display order of the forge filter options.
func ForgeOptions() []ForgeOpt {
	return []ForgeOpt{
		{"github", "GitHub"},
		{"gitlab", "GitLab"},
		{"forgejo", "Forgejo"},
		{"ado", "ADO"},
	}
}

// PresentForges returns the options for forges that have at least one entry
// in local or remote, in canonical order.
func PresentForges(local, remote []RepoEntry) []ForgeOpt {
	present := map[string]bool{}
	for _, rows := range [][]RepoEntry{local, remote} {
		for _, e := range rows {
			if f, _, _, _ := Parts(e); f != "" {
				present[f] = true
			}
		}
	}
	var out []ForgeOpt
	for _, o := range ForgeOptions() {
		if present[o.Key] {
			out = append(out, o)
		}
	}
	return out
}

// PickerList is nav's visible picker list: local entries, then remote entries
// not cloned locally, owner-disambiguated, scoped to forgeKey ("" = All) and
// filtered by query.
func PickerList(local, remote []RepoEntry, forgeKey, query string) []RepoEntry {
	all := append([]RepoEntry{}, local...)
	all = append(all, DedupRemote(local, remote)...)
	all = DisambiguateOwners(all, func(e RepoEntry, label string) RepoEntry {
		e.Label = label
		return e
	})
	if forgeKey != "" {
		scoped := make([]RepoEntry, 0, len(all))
		for _, e := range all {
			if MatchesForge(e, forgeKey) {
				scoped = append(scoped, e)
			}
		}
		all = scoped
	}
	return FilterRows(all, query)
}
