//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/bot"
	"github.com/freaxnx01/bridge/internal/telegram"
)

var botConfigPath string

var botCmd = &cobra.Command{
	Use:   "bot",
	Short: "Run the Telegram nav bot: nav's repo picker and dashboard as inline buttons",
	Long: `bot long-polls a Telegram bot and serves nav's repo picker and per-repo
dashboard as inline-keyboard buttons: open a dashboard, start/open a session
(the Remote Control link arrives as its own message), stop it, create a
worktree, start a session on an issue, clone a remote repo, create a repo.

Config: ~/.config/bridge/bot.env (mode 0600) with TELEGRAM_BOT_TOKEN=… and
BRIDGE_BOT_ALLOW=<telegram user id>[,<id>]. See docs/telegram-bot.md.`,
	Args: cobra.NoArgs,
	RunE: runBot,
}

func init() {
	botCmd.Flags().StringVar(&botConfigPath, "config", "", "bot.env path (default $XDG_CONFIG_HOME/bridge/bot.env, else ~/.config/bridge/bot.env)")
	rootCmd.AddCommand(botCmd)
}

// botConfig is the parsed bot.env.
type botConfig struct {
	Token  string
	Allow  []int64
	APIURL string // optional BRIDGE_BOT_API_URL (tests / proxies)
}

func defaultBotConfigPath(getenv func(string) string) string {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "bridge", "bot.env")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bridge", "bot.env")
}

// loadBotConfig reads KEY=VALUE lines from path. It refuses a missing file and
// one readable by group or others (it holds the bot token). The token is never
// part of an error message.
func loadBotConfig(path string) (botConfig, error) {
	info, err := os.Stat(path)
	if err != nil {
		return botConfig{}, fmt.Errorf("bridge bot: config %s not found — create it (mode 0600) with TELEGRAM_BOT_TOKEN and BRIDGE_BOT_ALLOW; see docs/telegram-bot.md: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return botConfig{}, fmt.Errorf("bridge bot: %s has mode %04o but holds the bot token — run: chmod 600 %s", path, perm, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return botConfig{}, fmt.Errorf("bridge bot: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // read-only
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		vals[strings.TrimSpace(k)] = v
	}
	if err := sc.Err(); err != nil {
		return botConfig{}, fmt.Errorf("bridge bot: read %s: %w", path, err)
	}
	cfg := botConfig{Token: vals["TELEGRAM_BOT_TOKEN"], APIURL: vals["BRIDGE_BOT_API_URL"]}
	if cfg.Token == "" {
		return botConfig{}, fmt.Errorf("bridge bot: TELEGRAM_BOT_TOKEN missing in %s", path)
	}
	for _, part := range strings.Split(vals["BRIDGE_BOT_ALLOW"], ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return botConfig{}, fmt.Errorf("bridge bot: BRIDGE_BOT_ALLOW in %s: %q is not a Telegram user id", path, part)
		}
		cfg.Allow = append(cfg.Allow, id)
	}
	if len(cfg.Allow) == 0 {
		return botConfig{}, fmt.Errorf("bridge bot: BRIDGE_BOT_ALLOW missing in %s (comma-separated Telegram user ids)", path)
	}
	return cfg, nil
}

func runBot(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true
	path := botConfigPath
	if path == "" {
		path = defaultBotConfigPath(os.Getenv)
	}
	cfg, err := loadBotConfig(path)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	var opts []telegram.Option
	if cfg.APIURL != "" {
		opts = append(opts, telegram.WithBaseURL(cfg.APIURL))
	}
	allow := make(map[int64]bool, len(cfg.Allow))
	for _, id := range cfg.Allow {
		allow[id] = true
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r := newBotRunner(telegram.New(cfg.Token, opts...), bot.Config{Allow: allow}, newBotEnv(), log)
	log.Info("bridge bot started", "allowlisted", len(allow))
	if err := r.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("bridge bot stopped")
	return nil
}

// chatEvent is an event for one chat; callbackID is set for taps.
type chatEvent struct {
	chatID     int64
	ev         bot.Event
	callbackID string
}

// chatSlot is the runner's per-chat bookkeeping around bot.State.
type chatSlot struct {
	state    bot.State
	msgID    int
	lastText string
	lastKB   string
}

// botRunner owns all chat state. Only run's goroutine touches chats;
// pollers and actions talk to it through events.
type botRunner struct {
	tg          *telegram.Client
	cfg         bot.Config
	env         botEnv
	log         *slog.Logger
	events      chan chatEvent
	chats       map[int64]*chatSlot
	pollTimeout time.Duration
}

func newBotRunner(tg *telegram.Client, cfg bot.Config, env botEnv, log *slog.Logger) *botRunner {
	return &botRunner{
		tg: tg, cfg: cfg, env: env, log: log,
		events:      make(chan chatEvent, 64),
		chats:       map[int64]*chatSlot{},
		pollTimeout: 50 * time.Second,
	}
}

// run serves until ctx ends.
func (r *botRunner) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ce := <-r.events:
			r.handle(ctx, ce)
		}
	}
}

// poll long-polls getUpdates forever. The client already retries 429/5xx;
// anything it gives up on (409 included) is logged and backed off here.
func (r *botRunner) poll(ctx context.Context) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		pctx, cancel := context.WithTimeout(ctx, r.pollTimeout+15*time.Second)
		ups, err := r.tg.GetUpdates(pctx, offset, r.pollTimeout)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, telegram.ErrConflict) {
				r.log.Warn("another process polls this token; backing off", "wait", backoff)
			} else {
				r.log.Warn("getUpdates failed", "err", err, "wait", backoff)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 60*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			offset = u.UpdateID + 1
			ce, ok := toChatEvent(u)
			if !ok {
				continue
			}
			select {
			case r.events <- ce:
			case <-ctx.Done():
				return
			}
		}
	}
}

func toChatEvent(u telegram.Update) (chatEvent, bool) {
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil:
		q := u.CallbackQuery
		return chatEvent{chatID: q.Message.Chat.ID, callbackID: q.ID, ev: bot.Tap{
			UserID: q.From.ID, ChatID: q.Message.Chat.ID, MessageID: q.Message.MessageID, Data: q.Data,
		}}, true
	case u.Message != nil && u.Message.From != nil && u.Message.Text != "":
		m := u.Message
		return chatEvent{chatID: m.Chat.ID, ev: bot.Text{UserID: m.From.ID, ChatID: m.Chat.ID, Text: m.Text}}, true
	}
	return chatEvent{}, false
}

// handle runs one event through bot.Update, executes the actions and
// re-renders the chat's message.
func (r *botRunner) handle(ctx context.Context, ce chatEvent) {
	slot := r.chats[ce.chatID]
	if slot == nil {
		slot = &chatSlot{}
	}
	next, actions := bot.Update(r.cfg, slot.state, ce.ev)
	for _, a := range actions {
		if d, ok := a.(bot.Drop); ok {
			r.log.Info("dropped update from a user not in BRIDGE_BOT_ALLOW", "user", d.UserID, "chat", d.ChatID)
			return // no state, no answer, no reply
		}
	}
	r.chats[ce.chatID] = slot
	slot.state = next
	toast, newMsg := "", false
	for _, a := range actions {
		switch a := a.(type) {
		case bot.Toast:
			toast = a.Text
		case bot.NewMessage:
			newMsg = true
		case bot.SendLink:
			r.send(ctx, ce.chatID, a.Text)
		case bot.Reply:
			r.send(ctx, ce.chatID, a.Text)
		default:
			r.dispatch(ctx, ce.chatID, a)
		}
	}
	if ce.callbackID != "" {
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := r.tg.AnswerCallbackQuery(actx, ce.callbackID, toast); err != nil {
			r.log.Warn("answerCallbackQuery failed", "err", err)
		}
		cancel()
	}
	if newMsg {
		r.sendScreen(ctx, ce.chatID, slot)
	} else if slot.msgID != 0 {
		r.editScreen(ctx, ce.chatID, slot)
	}
}

// dispatch executes a slow action off the event loop under its timeout and
// feeds the result back as an event — always, so Busy always clears.
func (r *botRunner) dispatch(ctx context.Context, chatID int64, a bot.Action) {
	timeout := actionTimeout(a)
	go func() {
		actx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ev := r.env.exec(actx, a)
		if ev == nil {
			return
		}
		select {
		case r.events <- chatEvent{chatID: chatID, ev: ev}:
		case <-ctx.Done():
		}
	}()
}

// actionTimeout: 2 min for clone, fetch, repo creation and a forge refresh; 30 s otherwise.
func actionTimeout(a bot.Action) time.Duration {
	switch a := a.(type) {
	case bot.Clone, bot.CreateRepo, bot.Fetch:
		return 2 * time.Minute
	case bot.LoadRepos:
		if a.Refresh {
			return 2 * time.Minute
		}
	}
	return 30 * time.Second
}

func toMarkup(kb bot.Keyboard) *telegram.InlineKeyboardMarkup {
	m := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{}}
	for _, row := range kb {
		out := make([]telegram.InlineKeyboardButton, 0, len(row))
		for _, b := range row {
			out = append(out, telegram.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data})
		}
		m.InlineKeyboard = append(m.InlineKeyboard, out)
	}
	return m
}

func kbKey(kb bot.Keyboard) string {
	b, _ := json.Marshal(kb) // plain structs; cannot fail
	return string(b)
}

func (r *botRunner) send(ctx context.Context, chatID int64, text string) {
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := r.tg.SendMessage(sctx, chatID, text, nil); err != nil {
		r.log.Warn("sendMessage failed", "err", err)
	}
}

func (r *botRunner) sendScreen(ctx context.Context, chatID int64, slot *chatSlot) {
	text, kb := bot.Render(slot.state)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m, err := r.tg.SendMessage(sctx, chatID, text, toMarkup(kb))
	if err != nil {
		r.log.Warn("sendMessage failed", "err", err)
		return
	}
	slot.msgID = m.MessageID
	slot.state, _ = bot.Update(r.cfg, slot.state, bot.MessageSent{MessageID: m.MessageID})
	slot.lastText, slot.lastKB = text, kbKey(kb)
}

func (r *botRunner) editScreen(ctx context.Context, chatID int64, slot *chatSlot) {
	text, kb := bot.Render(slot.state)
	key := kbKey(kb)
	if text == slot.lastText && key == slot.lastKB {
		return
	}
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := r.tg.EditMessageText(ectx, chatID, slot.msgID, text, toMarkup(kb))
	if err != nil && !errors.Is(err, telegram.ErrNotModified) {
		r.log.Warn("editMessageText failed", "err", err)
		return
	}
	slot.lastText, slot.lastKB = text, key
}
