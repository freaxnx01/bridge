//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeBotToken = "42:FAKE-TOKEN"

// tgCall is one sendMessage/editMessageText the bot made.
type tgCall struct {
	Method    string
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
	Text      string
	Markup    struct {
		Rows [][]struct {
			Text string `json:"text"`
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	} `json:"reply_markup"`
}

// fakeTelegram is a scripted Bot API: tests queue updates, the bot polls them
// and its replies are recorded.
type fakeTelegram struct {
	t       *testing.T
	mu      sync.Mutex
	queue   []map[string]any
	nextID  int64
	nextMsg int
	calls   []tgCall
	answers int
	srv     *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{t: t, nextID: 1, nextMsg: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	prefix := "/bot" + fakeBotToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	raw, _ := io.ReadAll(r.Body)
	reply := func(result any) {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		_, _ = w.Write(b)
	}
	switch method {
	case "getUpdates":
		var req struct {
			Offset int64 `json:"offset"`
		}
		_ = json.Unmarshal(raw, &req)
		deadline := time.Now().Add(300 * time.Millisecond)
		for {
			f.mu.Lock()
			var out []map[string]any
			for _, u := range f.queue {
				if u["update_id"].(int64) >= req.Offset {
					out = append(out, u)
				}
			}
			f.mu.Unlock()
			if len(out) > 0 || time.Now().After(deadline) {
				if out == nil {
					out = []map[string]any{}
				}
				reply(out)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "sendMessage", "editMessageText":
		var c tgCall
		if err := json.Unmarshal(raw, &c); err != nil {
			f.t.Errorf("%s body: %v", method, err)
		}
		c.Method = method
		f.mu.Lock()
		if method == "sendMessage" {
			f.nextMsg++
			c.MessageID = f.nextMsg
		}
		f.calls = append(f.calls, c)
		id := c.MessageID
		f.mu.Unlock()
		if method == "sendMessage" {
			reply(map[string]any{"message_id": id, "chat": map[string]any{"id": c.ChatID, "type": "group"}})
			return
		}
		reply(true)
	case "answerCallbackQuery":
		f.mu.Lock()
		f.answers++
		f.mu.Unlock()
		reply(true)
	default:
		f.t.Errorf("unexpected method %s", method)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeTelegram) push(u map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u["update_id"] = f.nextID
	f.nextID++
	f.queue = append(f.queue, u)
}

func (f *fakeTelegram) text(user, chat int64, msg string) {
	f.push(map[string]any{"message": map[string]any{
		"message_id": 1, "from": map[string]any{"id": user}, "chat": map[string]any{"id": chat, "type": "group"}, "text": msg,
	}})
}

func (f *fakeTelegram) tap(user, chat int64, msgID int, data string) {
	f.push(map[string]any{"callback_query": map[string]any{
		"id": fmt.Sprintf("cb%d", f.nextID), "from": map[string]any{"id": user}, "data": data,
		"message": map[string]any{"message_id": msgID, "chat": map[string]any{"id": chat, "type": "group"}},
	}})
}

// waitFor returns the first recorded call (from index from on) matching pred.
func (f *fakeTelegram) waitFor(from int, pred func(tgCall) bool) (tgCall, int) {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for i := from; i < len(f.calls); i++ {
			if pred(f.calls[i]) {
				c := f.calls[i]
				f.mu.Unlock()
				return c, i + 1
			}
		}
		f.mu.Unlock()
		time.Sleep(30 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t.Fatalf("no matching Telegram call; calls so far: %+v", f.calls)
	return tgCall{}, 0
}

func button(t *testing.T, c tgCall, match func(string) bool) string {
	t.Helper()
	for _, row := range c.Markup.Rows {
		for _, b := range row {
			if match(b.Text) {
				return b.Data
			}
		}
	}
	t.Fatalf("no matching button in %+v", c.Markup.Rows)
	return ""
}

func writeBotEnv(t *testing.T, apiURL string, allow string) string {
	t.Helper()
	path := t.TempDir() + "/bot.env"
	body := "TELEGRAM_BOT_TOKEN=" + fakeBotToken + "\nBRIDGE_BOT_ALLOW=" + allow + "\nBRIDGE_BOT_API_URL=" + apiURL + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// lockedBuffer is a bytes.Buffer safe to read while the child writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startBot(t *testing.T, cfgPath string, env []string) *lockedBuffer {
	t.Helper()
	cmd := bridgeCmd("bot", "--config", cfgPath)
	cmd.Env = env
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("bot stderr:\n%s", stderr.String())
		}
	})
	return stderr
}

func TestBotE2E_PickerDashboardStartSendsRCLink(t *testing.T) {
	bin, tmuxLog := fakeTmux(t)
	tg := newFakeTelegram(t)
	const me, chat = int64(111), int64(-5)
	stderr := startBot(t, writeBotEnv(t, tg.srv.URL, "111"),
		launchEnv(t, bin, "FAKE_TMUX_PANE=Remote Control: https://claude.ai/code/session_01E2E"))

	tg.text(me, chat, "/nav")
	// The picker is sent while repos load, then edited in place once loaded.
	pick, _ := tg.waitFor(0, func(c tgCall) bool {
		return strings.HasPrefix(c.Text, "🗂 bridge · repos") && strings.Contains(c.Text, "3 repos")
	})
	if pick.ChatID != chat || pick.MessageID == 0 {
		t.Errorf("picker = chat %d message %d, want chat %d", pick.ChatID, pick.MessageID, chat)
	}

	tg.tap(me, chat, pick.MessageID, button(t, pick, func(s string) bool { return s == "bridge" }))
	dash, next := tg.waitFor(0, func(c tgCall) bool {
		return c.Method == "editMessageText" && strings.HasPrefix(c.Text, "📁 bridge") && len(c.Markup.Rows) > 0 && strings.HasSuffix(c.Markup.Rows[0][0].Text, " ▶")
	})

	tg.tap(me, chat, dash.MessageID, button(t, dash, func(s string) bool { return strings.HasSuffix(s, " ▶") }))
	link, _ := tg.waitFor(next, func(c tgCall) bool {
		return c.Method == "sendMessage" && strings.Contains(c.Text, "https://claude.ai/code/session_01E2E")
	})
	if !strings.HasPrefix(link.Text, "🔗 bridge") {
		t.Errorf("link message = %q", link.Text)
	}
	log, _ := os.ReadFile(tmuxLog)
	if !strings.Contains(string(log), "new-session -d -s bridge") || !strings.Contains(string(log), "--remote-control") {
		t.Errorf("tmux log lacks a detached RC launch:\n%s", log)
	}
	if strings.Contains(stderr.String(), fakeBotToken) {
		t.Errorf("token logged:\n%s", stderr.String())
	}
}

func TestBotE2E_IgnoresNonAllowlistedUser(t *testing.T) {
	bin, _ := fakeTmux(t)
	tg := newFakeTelegram(t)
	stderr := startBot(t, writeBotEnv(t, tg.srv.URL, "111"), launchEnv(t, bin))

	tg.text(999, -5, "/nav")
	tg.text(111, -5, "/nav")
	tg.waitFor(0, func(c tgCall) bool { return c.Method == "sendMessage" })
	time.Sleep(300 * time.Millisecond)
	tg.mu.Lock()
	defer tg.mu.Unlock()
	sends := 0
	for _, c := range tg.calls {
		if c.Method == "sendMessage" {
			sends++
		}
	}
	if sends != 1 {
		t.Errorf("sendMessage calls = %d, want 1 (only the allowlisted user)", sends)
	}
	if !strings.Contains(stderr.String(), "user=999") {
		t.Errorf("drop not logged:\n%s", stderr.String())
	}
}

// Review Focus: `bridge bot` refuses a world-readable token file.
func TestBotE2E_RefusesWorldReadableConfig(t *testing.T) {
	path := writeBotEnv(t, "http://127.0.0.1:1", "111")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("bot", "--config", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err == nil || !errors.As(err, &ee) || ee.ExitCode() == 0 {
		t.Fatalf("want a non-zero exit, got %v", err)
	}
	if !strings.Contains(stderr.String(), "chmod 600") || strings.Contains(stderr.String(), fakeBotToken) {
		t.Errorf("stderr = %q", stderr.String())
	}
}
