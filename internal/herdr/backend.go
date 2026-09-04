package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/launcher"
)

// Live reports the agents Herdr currently hosts, as core.Session values keyed
// by the bridge slot id derived from each agent's working directory.
//
// State carries Herdr's lifecycle status verbatim (working, idle, blocked,
// done, unknown) rather than tmux's attached/detached. LastActivity stays the
// zero value: Herdr reports no timestamps.
func (c *Client) Live() ([]core.Session, error) {
	agents, err := c.agentList(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]core.Session, 0, len(agents))
	for _, a := range agents {
		// An entry with no agent field is a pane hosting no agent; it also
		// reports AgentStatus "unknown", so the field's presence is what
		// distinguishes the two.
		if a.Agent == "" {
			continue
		}
		slot := SlotIDForPath(a.Cwd)
		if slot == "" {
			continue
		}
		out = append(out, core.Session{
			SlotID:   slot,
			TmuxName: slot,
			State:    a.AgentStatus,
		})
	}
	return out, nil
}

// Attach focuses the Herdr tab hosting slot's agent. It returns a run plan, so
// nav stays on screen while focus moves to the agent's tab. Returns a wrapped
// ErrNoSession when no live agent matches the slot.
func (c *Client) Attach(slot string) (launcher.Plan, error) {
	tab, err := c.tabFor(context.Background(), slot)
	if err != nil {
		return launcher.Plan{}, err
	}
	return launcher.RunPlan(func(ctx context.Context) error {
		return c.call(ctx, nil, "tab", "focus", tab)
	}), nil
}

// tabFor returns the tab id hosting slot's agent, or a wrapped ErrNoSession.
func (c *Client) tabFor(ctx context.Context, slot string) (string, error) {
	agents, err := c.agentList(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Agent == "" {
			continue
		}
		if SlotIDForPath(a.Cwd) == slot {
			return a.TabID, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoSession, slot)
}

// startAttempts and defaultRetryDelay bound the wait for a freshly created
// pane to reach its interactive prompt. `herdr agent start` requires a settled
// shell, but a new pane is still running profile init — which on a host with
// direnv hooks can take a moment. Five attempts on a doubling delay from 250ms
// spans ~4s of shell startup, well inside a user's patience.
const (
	startAttempts     = 5
	defaultRetryDelay = 250 * time.Millisecond
)

// Launch opens a Herdr tab in dir running spec's agent, then focuses it.
//
// It is idempotent, as launcher.Backend requires: a slot whose agent is already
// live resolves as Attach would, because `herdr tab create` always creates and
// would otherwise leave a duplicate tab behind on every launch.
func (c *Client) Launch(slot, dir string, spec agents.AgentSpec) (launcher.Plan, error) {
	if slot == "" {
		return launcher.Plan{}, errors.New("herdr: empty slot")
	}
	if dir == "" {
		return launcher.Plan{}, errors.New("herdr: empty dir")
	}
	if spec.Bin == "" {
		return launcher.Plan{}, errors.New("herdr: agent has no Bin")
	}
	if plan, err := c.Attach(slot); err == nil {
		return plan, nil
	} else if !errors.Is(err, ErrNoSession) {
		return launcher.Plan{}, err
	}
	return launcher.RunPlan(func(ctx context.Context) error {
		tab, err := c.tabCreate(ctx, dir, slot)
		if err != nil {
			return err
		}
		// A GUI editor is not a Herdr agent kind: run it in the pane and let it
		// take focus itself.
		if _, ok := agentKinds[spec.Name]; !ok {
			// `pane run` hands this string to the pane's shell, so quote every
			// part. Unreachable today (the only non-agent spec is `code` with
			// Args ["."]), but the next agent added to the registry must not
			// have to rediscover it. Mirrors launcher's shellQuote.
			cmd := shellQuoteJoin(append([]string{spec.Bin}, spec.Args...))
			return c.call(ctx, nil, "pane", "run", tab.PaneID, cmd)
		}
		if err := c.startAgent(ctx, tab.PaneID, slot, spec); err != nil &&
			!errors.Is(err, ErrAgentNotReady) {
			// The tab is left in place: a shell in the right directory beats
			// nothing, and nav reports the error.
			return err
		}
		// Reached on success and on ErrAgentNotReady alike — in the latter case
		// the agent is up and waiting on a prompt, so the user must see it.
		return c.call(ctx, nil, "tab", "focus", tab.TabID)
	}), nil
}

// startAgent runs `herdr agent start`, retrying only while the pane reports
// ErrPaneBusy — i.e. it is genuinely still reaching its interactive prompt.
// Every other outcome returns immediately, including ErrAgentNotReady (the
// agent did start and is blocked, which retrying cannot improve) and any
// deterministic failure that a retry would only delay.
func (c *Client) startAgent(ctx context.Context, pane, slot string, spec agents.AgentSpec) error {
	live, err := c.agentList(ctx)
	if err != nil {
		return err
	}
	taken := make([]string, 0, len(live))
	for _, a := range live {
		if a.Agent != "" {
			taken = append(taken, a.Agent)
		}
	}
	args := []string{"agent", "start", agentName(slot, taken), "--kind", agentKinds[spec.Name], "--pane", pane}
	if len(spec.Args) > 0 {
		args = append(args, "--")
		args = append(args, spec.Args...)
	}
	delay := c.retryDelay
	if delay <= 0 {
		delay = defaultRetryDelay
	}
	var lastErr error
	for attempt := 0; attempt < startAttempts; attempt++ {
		lastErr = c.call(ctx, nil, args...)
		// Retry ONLY a busy pane. Everything else — a bad --kind, a stale pane
		// id, a missing workspace, a CLI usage error — is deterministic, so
		// repeating it just delays the real error by the whole backoff budget.
		if !errors.Is(lastErr, ErrPaneBusy) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return fmt.Errorf("herdr: pane never became available after %d attempts: %w", startAttempts, lastErr)
}

// shellQuoteJoin joins parts into a single shell-safe command string, quoting
// any part that is not already a bare word.
func shellQuoteJoin(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = shellQuote(p)
	}
	return strings.Join(q, " ")
}

// shellQuote single-quotes s unless every rune is shell-safe. Mirrors
// internal/launcher's shellQuote, kept local so herdr does not depend on
// launcher for a string utility.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '+' || r == '=' || r == ',') {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}
