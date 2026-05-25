package core

import (
	"encoding/json"
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

func LoadSlots(path string) ([]Slot, error) {
	b, err := store.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var f slotFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Slots, nil
}
