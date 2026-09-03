// Package herdr starts and discovers agent sessions in a Herdr session, via
// the herdr CLI. It implements launcher.Backend, so nav can host agents as
// Herdr tabs — recognized by `herdr agent list` and its lifecycle states —
// instead of wrapping them in tmux.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Sentinel errors callers match with errors.Is.
var (
	// ErrNoSession reports that no live agent matches the requested slot.
	ErrNoSession = errors.New("herdr: no live session for slot")
	// ErrAgentNotReady reports that an agent launched but is blocked on a
	// prompt or dialog. The agent exists; it needs user input.
	ErrAgentNotReady = errors.New("herdr: agent started but is not ready")
	// ErrCLIUsage reports a malformed command line — a bridge bug, not a
	// Herdr outage.
	ErrCLIUsage = errors.New("herdr: cli usage error")
)

// ExitError carries a herdr CLI exit status. Exit 1 is a server error whose
// body is a JSON envelope; exit 2 is a CLI syntax error.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return fmt.Sprintf("herdr: exit %d", e.Code) }
func (e *ExitError) Unwrap() error { return e.Err }

// Runner executes a herdr CLI subcommand and returns its stdout. Injected so
// the backend is testable without a Herdr server.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Client talks to the running Herdr server through the CLI.
type Client struct {
	Run Runner
	// Workspace pins every created tab to nav's own workspace, so a tab never
	// lands in whichever workspace another Herdr client happens to focus.
	Workspace string
}

// New returns a Client driving the herdr binary named by $HERDR_BIN_PATH,
// falling back to "herdr" on $PATH, pinned to $HERDR_WORKSPACE_ID.
func New() *Client {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return &Client{
		Workspace: os.Getenv("HERDR_WORKSPACE_ID"),
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			out, err := exec.CommandContext(ctx, bin, args...).Output()
			if err == nil {
				return out, nil
			}
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				// Server errors put their JSON envelope on stderr.
				body := out
				if len(body) == 0 {
					body = ee.Stderr
				}
				return body, &ExitError{Code: ee.ExitCode(), Err: err}
			}
			return nil, fmt.Errorf("herdr: run %s: %w", strings.Join(args, " "), err)
		},
	}
}

// envelope is the shared herdr CLI response shape.
type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// agentInfo is one entry of `herdr agent list`. Agent is empty for a pane with
// no agent, which is how a real agent is told apart from an idle shell — a
// bare pane also reports AgentStatus "unknown".
type agentInfo struct {
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	Cwd         string `json:"cwd"`
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
}

// tabCreated is the useful subset of `herdr tab create`.
type tabCreated struct {
	PaneID string
	TabID  string
	Label  string
}

// call runs one subcommand and unwraps its envelope into out.
func (c *Client) call(ctx context.Context, out any, args ...string) error {
	body, runErr := c.Run(ctx, args...)
	var ex *ExitError
	if runErr != nil && !errors.As(runErr, &ex) {
		return runErr
	}
	if ex != nil && ex.Code == 2 {
		return fmt.Errorf("%w: %s: %s", ErrCLIUsage, strings.Join(args, " "), strings.TrimSpace(string(body)))
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		if ex != nil {
			return fmt.Errorf("herdr: %s failed: %w", strings.Join(args, " "), ex)
		}
		return fmt.Errorf("herdr: decode %s: %w", strings.Join(args, " "), err)
	}
	if env.Error != nil {
		if env.Error.Code == "agent_not_ready" {
			return fmt.Errorf("%w: %s", ErrAgentNotReady, env.Error.Message)
		}
		return fmt.Errorf("herdr: %s: %s (%s)", strings.Join(args, " "), env.Error.Message, env.Error.Code)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("herdr: decode %s result: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (c *Client) agentList(ctx context.Context) ([]agentInfo, error) {
	var res struct {
		Agents []agentInfo `json:"agents"`
	}
	if err := c.call(ctx, &res, "agent", "list"); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

func (c *Client) tabCreate(ctx context.Context, dir, label string) (tabCreated, error) {
	var res struct {
		RootPane struct {
			PaneID string `json:"pane_id"`
			TabID  string `json:"tab_id"`
		} `json:"root_pane"`
		Tab struct {
			TabID string `json:"tab_id"`
			Label string `json:"label"`
		} `json:"tab"`
	}
	args := []string{"tab", "create"}
	if c.Workspace != "" {
		args = append(args, "--workspace", c.Workspace)
	}
	args = append(args, "--cwd", dir, "--label", label, "--no-focus")
	if err := c.call(ctx, &res, args...); err != nil {
		return tabCreated{}, err
	}
	if res.RootPane.PaneID == "" {
		return tabCreated{}, errors.New("herdr: tab create returned no root pane")
	}
	return tabCreated{PaneID: res.RootPane.PaneID, TabID: res.Tab.TabID, Label: res.Tab.Label}, nil
}
