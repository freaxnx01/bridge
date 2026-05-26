package core

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// bashSlotEntry mirrors the on-disk shape written by the legacy bash bridge.
// Nullable worktree → *string. session is the tmux session name.
type bashSlotEntry struct {
	Repo      string  `json:"repo"`
	Worktree  *string `json:"worktree"`
	PID       int     `json:"pid"`
	StartedAt int64   `json:"started_at"`
	Session   string  `json:"session"`
}

// LoadSlots reads the slot registry. Tolerates two on-disk shapes:
//   - Go format: {"slots": [<Slot>, ...]}
//   - Bash format (legacy): {"slots": {"<index>": <bashSlotEntry>|null, ...}}
//
// Empty / missing / null "slots" returns (nil, nil).
func LoadSlots(path string) ([]Slot, error) {
	b, err := store.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, err
	}
	// Peek at the value type of "slots" to pick the parser.
	var probe struct {
		Slots json.RawMessage `json:"slots"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(probe.Slots)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	switch trimmed[0] {
	case '[':
		var f slotFile
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, err
		}
		return f.Slots, nil
	case '{':
		var bf struct {
			Slots map[string]*bashSlotEntry `json:"slots"`
		}
		if err := json.Unmarshal(b, &bf); err != nil {
			return nil, err
		}
		out := make([]Slot, 0, len(bf.Slots))
		for _, e := range bf.Slots {
			if e == nil {
				continue
			}
			wt := ""
			if e.Worktree != nil {
				wt = *e.Worktree
			}
			out = append(out, Slot{
				ID:       e.Session,
				Repo:     e.Repo,
				Worktree: wt,
				Agent:    "", // bash format does not track agent name
				Created:  time.Unix(e.StartedAt, 0).UTC(),
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("slots: unexpected JSON shape (first byte %q)", trimmed[0])
	}
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
