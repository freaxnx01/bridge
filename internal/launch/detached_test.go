//go:build !windows

package launch

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
)

// fakeExec records every argv and answers capture-pane with pane.
type fakeExec struct {
	mu     sync.Mutex
	calls  [][]string
	pane   string
	failOn string // argv[1] that fails ("" = none)
}

func (f *fakeExec) run(_ context.Context, argv []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, argv)
	if len(argv) > 1 && argv[1] == f.failOn {
		return []byte("boom"), errors.New("exit status 1")
	}
	if len(argv) > 1 && argv[1] == "capture-pane" {
		return []byte(f.pane), nil
	}
	return nil, nil
}

func liveSlots(ids ...string) func() ([]core.Session, error) {
	return func() ([]core.Session, error) {
		var out []core.Session
		for _, id := range ids {
			out = append(out, core.Session{SlotID: id})
		}
		return out, nil
	}
}

func claudeTarget(t *testing.T) Target {
	spec, _ := agents.Resolve("claude")
	return Target{Repo: core.Repo{Name: "bridge"}, WorkDir: t.TempDir(), Slot: "bridge", AgentName: "claude", Spec: spec, HasAgent: true}
}

func TestStartDetached_NewSessionWithRC(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	fx := &fakeExec{}
	d := Deps{Exec: fx.run, Live: liveSlots(), Getenv: env(nil)}
	tg := claudeTarget(t)
	running, err := StartDetached(context.Background(), d, &tg, true, true)
	if err != nil || running {
		t.Fatalf("running=%v err=%v", running, err)
	}
	if len(fx.calls) != 1 || fx.calls[0][0] != "sh" {
		t.Fatalf("calls = %v, want one sh -c", fx.calls)
	}
	body := fx.calls[0][2]
	if !strings.Contains(body, "new-session -d -s bridge") || !strings.Contains(body, "--remote-control") || !strings.Contains(body, "-n bridge") {
		t.Errorf("body = %s", body)
	}
}

func TestStartDetached_AlreadyRunningTouchesNothing(t *testing.T) {
	fx := &fakeExec{}
	d := Deps{Exec: fx.run, Live: liveSlots("bridge")}
	tg := claudeTarget(t)
	running, err := StartDetached(context.Background(), d, &tg, false, true)
	if err != nil || !running || len(fx.calls) != 0 {
		t.Errorf("running=%v err=%v calls=%v", running, err, fx.calls)
	}
}

func TestStartDetached_RCNeedsClaude(t *testing.T) {
	fx := &fakeExec{}
	spec, _ := agents.Resolve("code")
	tg := Target{Slot: "x", WorkDir: t.TempDir(), Spec: spec}
	_, err := StartDetached(context.Background(), Deps{Exec: fx.run}, &tg, true, true)
	var rc RCAgentError
	if !errors.As(err, &rc) || err.Error() != "bridge: --rc needs the claude agent (got code)" || len(fx.calls) != 0 {
		t.Errorf("err = %v calls = %v", err, fx.calls)
	}
}

func TestStartDetached_TmuxFailureWrapped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	fx := &fakeExec{failOn: "-c"}
	tg := claudeTarget(t)
	_, err := StartDetached(context.Background(), Deps{Exec: fx.run, Getenv: env(nil)}, &tg, false, true)
	if err == nil || !strings.HasPrefix(err.Error(), "tmux: ") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

func TestWaitRCURL_LastMatchWins(t *testing.T) {
	fx := &fakeExec{pane: "old https://claude.ai/code/session_OLD1\nnew https://claude.ai/code/session_NEW2"}
	got := WaitRCURL(context.Background(), Deps{Exec: fx.run, Live: liveSlots("s")}, "s", time.Second)
	if got != "https://claude.ai/code/session_NEW2" {
		t.Errorf("got %q", got)
	}
}

func TestWaitRCURL_SessionGoneReturnsEarly(t *testing.T) {
	fx := &fakeExec{failOn: "capture-pane"}
	start := time.Now()
	got := WaitRCURL(context.Background(), Deps{Exec: fx.run, Live: liveSlots()}, "s", 5*time.Second)
	if got != "" || time.Since(start) > time.Second {
		t.Errorf("got %q after %s; want \"\" immediately", got, time.Since(start))
	}
}

func TestWaitRCURL_IgnoresNonSessionURLAndTimesOut(t *testing.T) {
	fx := &fakeExec{pane: "see https://claude.ai/code/artifact/abc"}
	d := Deps{Exec: fx.run, Live: liveSlots("s"), PollEvery: 10 * time.Millisecond}
	if got := WaitRCURL(context.Background(), d, "s", 50*time.Millisecond); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestStop(t *testing.T) {
	fx := &fakeExec{}
	if err := Stop(context.Background(), Deps{Exec: fx.run}, "bridge-wt-x"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tmux", "kill-session", "-t", "bridge-wt-x"}; !slices.Equal(fx.calls[0], want) {
		t.Errorf("argv = %v, want %v", fx.calls[0], want)
	}
	if err := Stop(context.Background(), Deps{Exec: fx.run}, ""); err == nil {
		t.Error("empty slot must fail")
	}
}
