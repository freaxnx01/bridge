package core

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Session struct {
	SlotID       string        `json:"slot_id"`
	State        string        `json:"state"`
	Age          time.Duration `json:"age"`
	LastActivity time.Time     `json:"last_activity"`
	PID          int           `json:"pid,omitempty"`
	TmuxName     string        `json:"tmux_name"`
}

// ParseTmuxList parses tmux ls output in format "name|attached|created_unix|activity_unix".
func ParseTmuxList(raw string, nowUnix int64) ([]Session, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []Session
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 4 {
			return nil, fmt.Errorf("malformed tmux line: %q", line)
		}
		attached, _ := strconv.Atoi(parts[1])
		created, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("created: %w", err)
		}
		activity, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("activity: %w", err)
		}
		state := "detached"
		if attached > 0 {
			state = "attached"
		}
		out = append(out, Session{
			SlotID:       parts[0],
			TmuxName:     parts[0],
			State:        state,
			Age:          time.Duration(nowUnix-created) * time.Second,
			LastActivity: time.Unix(activity, 0),
		})
	}
	return out, nil
}

// LiveSessions calls tmux and returns active sessions. Returns empty if tmux missing.
func LiveSessions() ([]Session, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}|#{session_attached}|#{session_created}|#{session_activity}")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return ParseTmuxList(string(out), time.Now().Unix())
}
