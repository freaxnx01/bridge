package nav

import "github.com/freaxnx01/bridge/internal/navdata"

// Entry exposes a picker row to navdata's generic list helpers.
func (r repoRow) Entry() navdata.RepoEntry {
	return navdata.RepoEntry{Label: r.label, Repo: r.repo, Remote: r.remote}
}

// withLabel returns a copy of r with a new label (navdata.DisambiguateOwners).
func (r repoRow) withLabel(label string) repoRow {
	r.label = label
	return r
}

// toRepoRows converts navdata entries to picker rows (issue counts pending).
func toRepoRows(es []navdata.RepoEntry) []repoRow {
	if es == nil {
		return nil
	}
	rows := make([]repoRow, 0, len(es))
	for _, e := range es {
		rows = append(rows, repoRow{label: e.Label, repo: e.Repo, remote: e.Remote})
	}
	return rows
}

// fromDashRow converts a navdata row; git status starts pending.
func fromDashRow(d navdata.DashRow) dashRow {
	return dashRow{
		worktree:     d.Worktree,
		branch:       d.Branch,
		path:         d.Path,
		agent:        d.Agent,
		slotID:       d.SlotID,
		state:        d.State,
		lastAccessed: d.LastAccessed,
		hasSession:   d.HasSession,
		displayLabel: d.DisplayLabel,
		isBase:       d.IsBase,
		dirtyState:   loadPending,
	}
}

func fromDashRows(ds []navdata.DashRow) []dashRow {
	rows := make([]dashRow, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, fromDashRow(d))
	}
	return rows
}

func toDirtyInfo(d navdata.Dirty) dirtyInfo {
	return dirtyInfo{modified: d.Modified, ahead: d.Ahead, behind: d.Behind, noUpstream: d.NoUpstream, clean: d.Clean}
}

func fromSessionRows(rs []navdata.SessionRow) []sessionRow {
	rows := make([]sessionRow, 0, len(rs))
	for _, r := range rs {
		rows = append(rows, sessionRow{
			slotID: r.SlotID, repoLabel: r.RepoLabel, worktree: r.Worktree,
			agent: r.Agent, state: r.State, lastAccessed: r.LastAccessed,
		})
	}
	return rows
}
