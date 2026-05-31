package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

type Slot struct {
	ID       string    `json:"id"`
	Repo     string    `json:"repo"`
	Worktree string    `json:"worktree,omitempty"`
	Agent    string    `json:"agent"`
	Created  time.Time `json:"created"`
}

type slotFile struct {
	Slots []Slot `json:"slots"`
}

// SlotID is the deterministic tmux session name / slot id for a repo and
// optional worktree: "<repo>" or "<repo>-wt-<worktree>". It is the single source
// of truth shared by the launch (cmd/bridge) and navigator (internal/nav) paths.
func SlotID(repoName, worktree string) string {
	id := repoName
	if worktree != "" {
		id += "-wt-" + worktree
	}
	return id
}

// LoadSlots reads the slot registry written by WriteSlots.
// Empty / missing / null "slots" returns (nil, nil).
//
// Recovery: a slot file written in the legacy bash-bridge shape (top-level
// object map) or otherwise unparseable is renamed to "<path>.legacy-<ts>.bak"
// and an empty registry is returned. The data is ephemeral cached state, so
// losing it on first Go invocation is harmless; the alternative is failing
// every UpsertSlot call on the upgrade host. The .bak is left in place so
// the user can inspect it if curious.
func LoadSlots(path string) ([]Slot, error) {
	b, err := store.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var f slotFile
	if err := json.Unmarshal(b, &f); err != nil {
		backup := path + ".legacy-" + time.Now().UTC().Format("20060102-150405") + ".bak"
		_ = os.Rename(path, backup)
		return nil, nil
	}
	return f.Slots, nil
}

// WriteSlots atomically writes the slot registry to path in Go format
// ({"slots":[...]}). Concurrent writers serialize via flock on "<path>.lock".
func WriteSlots(path string, slots []Slot) error {
	if slots == nil {
		slots = []Slot{}
	}
	lock, err := store.AcquireLock(filepath.Dir(path) + "/slots.lock")
	if err != nil {
		return err
	}
	defer lock.Release()
	return writeSlotsLocked(path, slots)
}

func writeSlotsLocked(path string, slots []Slot) error {
	b, err := json.MarshalIndent(slotFile{Slots: slots}, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

// PruneSlots partitions slots into (kept, dropped) based on whether each
// slot's ID matches any live session's SlotID. Pure function — used by the
// `bridge slots prune` command. Slot IDs are tmux session names, so the
// match is direct equality.
func PruneSlots(slots []Slot, sessions []Session) (kept, dropped []Slot) {
	live := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		live[s.SlotID] = true
	}
	for _, s := range slots {
		if live[s.ID] {
			kept = append(kept, s)
		} else {
			dropped = append(dropped, s)
		}
	}
	return
}

// UpsertSlot adds slot to the registry, or replaces an existing entry whose
// ID matches. Read-modify-write is performed under the same flock used by
// WriteSlots, so concurrent UpsertSlot calls do not lose entries.
func UpsertSlot(path string, slot Slot) error {
	lock, err := store.AcquireLock(filepath.Dir(path) + "/slots.lock")
	if err != nil {
		return err
	}
	defer lock.Release()
	existing, err := LoadSlots(path)
	if err != nil {
		return err
	}
	replaced := false
	for i, s := range existing {
		if s.ID == slot.ID {
			existing[i] = slot
			replaced = true
			break
		}
	}
	if !replaced {
		existing = append(existing, slot)
	}
	return writeSlotsLocked(path, existing)
}
