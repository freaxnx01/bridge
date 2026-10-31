//go:build !windows

package launch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/launcher"
)

// rcURLPattern matches the Remote Control link claude prints in its pane.
// Compiled once; *regexp.Regexp is immutable and safe for concurrent use.
var rcURLPattern = regexp.MustCompile(`https://claude\.ai/code/session_[A-Za-z0-9]+`)

// StartDetached starts t in a detached tmux session unless its slot is
// already live (alreadyRunning=true, nothing touched). With rc it requires
// the claude agent (RCAgentError otherwise) and adds --remote-control.
// Finalize runs only for a new session.
func StartDetached(ctx context.Context, d Deps, t *Target, rc, noSync bool) (alreadyRunning bool, err error) {
	if rc {
		if t.Spec.Name != "claude" {
			return false, RCAgentError{Agent: t.Spec.Name}
		}
		if !slices.Contains(t.Spec.Args, "--remote-control") {
			t.Spec.Args = append(slices.Clone(t.Spec.Args), "--remote-control")
		}
	}
	if SessionLive(d, t.Slot) {
		return true, nil
	}
	Finalize(ctx, d, t, noSync)
	argv, err := launcher.Tmux{}.LaunchArgvDetached(t.Slot, t.WorkDir, t.Spec)
	if err != nil {
		return false, fmt.Errorf("launch: %w", err)
	}
	if out, err := d.run(ctx, argv); err != nil {
		return false, fmt.Errorf("tmux: %w: %s", err, out)
	}
	return false, nil
}

// WaitRCURL polls slot's pane for the Remote Control link for up to wait.
// Best effort: "" on timeout, on ctx end, or as soon as the session is gone.
// The last match wins (scrollback may hold links from an earlier run).
func WaitRCURL(ctx context.Context, d Deps, slot string, wait time.Duration) string {
	poll := d.PollEvery
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		out, err := d.run(ctx, []string{"tmux", "capture-pane", "-p", "-J", "-t", slot, "-S", "-200"})
		if err != nil {
			if !SessionLive(d, slot) {
				return ""
			}
		} else if ms := rcURLPattern.FindAllString(string(out), -1); len(ms) > 0 {
			return ms[len(ms)-1]
		}
		if time.Now().After(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(poll):
		}
	}
}

// Stop kills slot's tmux session.
func Stop(ctx context.Context, d Deps, slot string) error {
	if slot == "" {
		return errors.New("launch: empty slot")
	}
	if out, err := d.run(ctx, []string{"tmux", "kill-session", "-t", slot}); err != nil {
		return fmt.Errorf("tmux kill-session %s: %w: %s", slot, err, strings.TrimSpace(string(out)))
	}
	return nil
}
