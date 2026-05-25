package core

import (
	"encoding/json"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

type Presence struct {
	Mode      string            `json:"mode"`
	Overrides map[string]string `json:"overrides,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
}

func LoadPresence(path string) (Presence, error) {
	b, err := store.ReadFile(path)
	if err != nil {
		return Presence{Mode: "auto"}, err
	}
	if len(b) == 0 {
		return Presence{Mode: "auto"}, nil
	}
	var p Presence
	if err := json.Unmarshal(b, &p); err != nil {
		return Presence{Mode: "auto"}, err
	}
	if p.Mode == "" {
		p.Mode = "auto"
	}
	return p, nil
}
