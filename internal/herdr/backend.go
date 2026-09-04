package herdr

import (
	"context"
	"fmt"

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
