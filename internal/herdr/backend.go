package herdr

import (
	"context"

	"github.com/freaxnx01/bridge/internal/core"
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
