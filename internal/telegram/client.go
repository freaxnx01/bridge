// Package telegram is a minimal Telegram Bot API client over net/http:
// long-poll getUpdates, sendMessage, editMessageText and answerCallbackQuery,
// with capped-backoff retries on 429/5xx/network errors. The bot token is part
// of every request URL, so transport errors are stripped of their URL before
// they are returned: the token never reaches an error string or a log line.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

var (
	// ErrConflict matches a 409: another process is polling this token.
	ErrConflict = errors.New("telegram: conflict: another process polls this token")
	// ErrNotModified matches editMessageText's "message is not modified" 400.
	ErrNotModified = errors.New("telegram: message is not modified")
)

// APIError is a non-ok Bot API response.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration // from parameters.retry_after on a 429
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Is maps API errors onto ErrConflict and ErrNotModified for errors.Is.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrConflict:
		return e.Code == http.StatusConflict
	case ErrNotModified:
		return e.Code == http.StatusBadRequest && strings.Contains(e.Description, "message is not modified")
	}
	return false
}

// transportError is a network-level failure (retried).
type transportError struct {
	method string
	err    error
}

func (e *transportError) Error() string { return "telegram " + e.method + ": " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// Client talks to the Bot API. Safe for concurrent use.
type Client struct {
	token      string
	baseURL    string
	hc         *http.Client
	minBackoff time.Duration
	maxBackoff time.Duration
	sleep      func(context.Context, time.Duration) error
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another API root (tests, proxies).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithBackoff sets the first retry delay and the cap (defaults 1s, 60s).
func WithBackoff(min, max time.Duration) Option {
	return func(c *Client) { c.minBackoff, c.maxBackoff = min, max }
}

// WithSleep replaces the retry sleep (tests record delays instead of waiting).
func WithSleep(fn func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = fn }
}

// New returns a client for token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:      token,
		baseURL:    defaultBaseURL,
		hc:         &http.Client{},
		minBackoff: time.Second,
		maxBackoff: 60 * time.Second,
		sleep:      sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// GetUpdates long-polls for updates with update_id >= offset, waiting up to
// timeout server-side. Only messages and callback queries are requested.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	req := struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{offset, int(timeout / time.Second), []string{"message", "callback_query"}}
	var ups []Update
	if err := c.call(ctx, "getUpdates", req, &ups); err != nil {
		return nil, err
	}
	return ups, nil
}

// SendMessage sends plain text (no parse mode) with an optional keyboard.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboardMarkup) (Message, error) {
	req := struct {
		ChatID      int64                 `json:"chat_id"`
		Text        string                `json:"text"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}{chatID, text, kb}
	var m Message
	err := c.call(ctx, "sendMessage", req, &m)
	return m, err
}

// EditMessageText replaces a message's text and keyboard.
func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, kb *InlineKeyboardMarkup) error {
	req := struct {
		ChatID      int64                 `json:"chat_id"`
		MessageID   int                   `json:"message_id"`
		Text        string                `json:"text"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}{chatID, messageID, text, kb}
	return c.call(ctx, "editMessageText", req, nil)
}

// AnswerCallbackQuery stops the tapped button's spinner, optionally with a toast.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	req := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
	}{callbackID, text}
	return c.call(ctx, "answerCallbackQuery", req, nil)
}

// call POSTs req as JSON, retrying 429/5xx/network failures with doubling
// backoff capped at maxBackoff (a 429's retry_after wins, also capped) until
// it succeeds, a non-retryable error occurs, or ctx ends.
func (c *Client) call(ctx context.Context, method string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}
	backoff := c.minBackoff
	for {
		err := c.once(ctx, method, body, out)
		if err == nil {
			return nil
		}
		wait, retry := c.retryDelay(err, backoff)
		if !retry || ctx.Err() != nil {
			return err
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return fmt.Errorf("%w (retry aborted: %w)", err, serr)
		}
		backoff = min(backoff*2, c.maxBackoff)
	}
}

func (c *Client) retryDelay(err error, backoff time.Duration) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == http.StatusTooManyRequests:
			wait := backoff
			if apiErr.RetryAfter > 0 {
				wait = apiErr.RetryAfter
			}
			return min(wait, c.maxBackoff), true
		case apiErr.Code >= 500:
			return backoff, true
		}
		return 0, false
	}
	var te *transportError
	if errors.As(err, &te) {
		return backoff, true
	}
	return 0, false
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) once(ctx context.Context, method string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: build request: %w", method, stripURL(err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("telegram %s: %w", method, ctx.Err())
		}
		return &transportError{method: method, err: stripURL(err)}
	}
	defer func() { _ = resp.Body.Close() }() // read-only body; close error carries no information
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &transportError{method: method, err: stripURL(err)}
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return &APIError{Method: method, Code: resp.StatusCode, Description: http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("telegram %s: decode response (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if !env.OK {
		e := &APIError{Method: method, Code: env.ErrorCode, Description: env.Description}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		if env.Parameters != nil && env.Parameters.RetryAfter > 0 {
			e.RetryAfter = time.Duration(env.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// stripURL drops the request URL (which embeds the token) from a *url.Error.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}
