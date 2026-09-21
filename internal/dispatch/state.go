package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/freaxnx01/bridge/internal/store"
)

// ReadState loads the dispatcher's local state. A missing file is the
// first-run case, not an error.
func ReadState(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

func WriteState(path string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

// DispatchesSince returns how many dispatches the current counting period has
// already spent, where since is the start of the window occurrence being
// bounded. A counter recorded before that boundary belongs to an earlier
// occurrence and does not carry over.
//
// The boundary is passed in rather than derived from the clock on purpose: an
// earlier version bucketed on a hardcoded 12:00 pivot, which was correct only
// while dispatch ran at night. Once the configured windows tile the whole day,
// a morning tick resolved to the previous night and inherited its spent
// counter, refusing every daytime candidate with "night cap N/N" while the
// budget rung had full headroom.
func (s State) DispatchesSince(since time.Time) int {
	// A zero boundary means the caller has no window occurrence to attribute
	// the counter to — during a budget-rung window, or in a schedule gap.
	// Returning the raw counter there would report last night's spend as this
	// window's.
	if since.IsZero() || s.NightStartedAt.IsZero() || s.NightStartedAt.Before(since) {
		return 0
	}
	return s.DispatchedTonight
}

// DispatchesInLane returns how many dispatches lane's current window
// occurrence has already spent. since is that occurrence's start: a counter
// recorded before it belongs to an earlier occurrence and does not carry over,
// the same rule DispatchesSince applies to the nightly counter.
func (s State) DispatchesInLane(lane string, since time.Time) int {
	ls, ok := s.Lanes[lane]
	if !ok || since.IsZero() || ls.StartedAt.IsZero() || ls.StartedAt.Before(since) {
		return 0
	}
	return ls.Dispatched
}

// RecordLaneDispatch adds n dispatches to lane's counter, restarting it when
// the stored counter belongs to an earlier window occurrence.
func (s *State) RecordLaneDispatch(lane string, since, now time.Time, n int) {
	if s.Lanes == nil {
		s.Lanes = make(map[string]LaneState)
	}
	base := s.DispatchesInLane(lane, since)
	ls := s.Lanes[lane]
	if base == 0 {
		ls.StartedAt = now
	}
	ls.Dispatched = base + n
	s.Lanes[lane] = ls
}
