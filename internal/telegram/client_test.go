package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456:SECRET-token"

// apiServer serves canned responses in order and records request bodies.
type apiServer struct {
	t         *testing.T
	mu        sync.Mutex
	responses []response
	paths     []string
	bodies    []map[string]any
}

type response struct {
	status int
	body   string
}

func newAPIServer(t *testing.T, rs ...response) (*apiServer, *httptest.Server) {
	a := &apiServer{t: t, responses: rs}
	srv := httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *apiServer) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	a.paths = append(a.paths, r.URL.Path)
	a.bodies = append(a.bodies, body)
	if len(a.responses) == 0 {
		a.t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	res := a.responses[0]
	a.responses = a.responses[1:]
	w.WriteHeader(res.status)
	_, _ = io.WriteString(w, res.body)
}

// recordSleeps returns a sleep func that records delays without waiting.
func recordSleeps(into *[]time.Duration) Option {
	return WithSleep(func(_ context.Context, d time.Duration) error {
		*into = append(*into, d)
		return nil
	})
}

func TestGetUpdates_OffsetTimeoutAndDecode(t *testing.T) {
	a, srv := newAPIServer(t, response{200, `{"ok":true,"result":[
		{"update_id":42,"message":{"message_id":1,"from":{"id":7},"chat":{"id":-100,"type":"group"},"text":"/nav"}},
		{"update_id":43,"callback_query":{"id":"cb1","from":{"id":7},"message":{"message_id":9,"chat":{"id":-100,"type":"group"}},"data":"3:r:1"}}]}`})
	c := New(testToken, WithBaseURL(srv.URL))
	ups, err := c.GetUpdates(context.Background(), 42, 50*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.paths[0] != "/bot"+testToken+"/getUpdates" {
		t.Errorf("path = %s", a.paths[0])
	}
	if a.bodies[0]["offset"] != float64(42) || a.bodies[0]["timeout"] != float64(50) {
		t.Errorf("body = %v", a.bodies[0])
	}
	if len(ups) != 2 || ups[0].Message.Text != "/nav" || ups[0].Message.From.ID != 7 || ups[0].Message.Chat.ID != -100 {
		t.Fatalf("updates = %+v", ups)
	}
	cq := ups[1].CallbackQuery
	if cq == nil || cq.ID != "cb1" || cq.Data != "3:r:1" || cq.Message.MessageID != 9 || cq.From.ID != 7 {
		t.Errorf("callback = %+v", cq)
	}
}

func TestSendMessage_KeyboardAndResult(t *testing.T) {
	a, srv := newAPIServer(t, response{200, `{"ok":true,"result":{"message_id":55,"chat":{"id":1,"type":"private"},"text":"hi"}}`})
	c := New(testToken, WithBaseURL(srv.URL))
	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "A", CallbackData: "1:r:0"}}}}
	m, err := c.SendMessage(context.Background(), 1, "hi", kb)
	if err != nil || m.MessageID != 55 {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
	want := map[string]any{"chat_id": float64(1), "text": "hi", "reply_markup": map[string]any{
		"inline_keyboard": []any{[]any{map[string]any{"text": "A", "callback_data": "1:r:0"}}}}}
	if !reflect.DeepEqual(a.bodies[0], want) {
		t.Errorf("body = %v, want %v", a.bodies[0], want)
	}
}

func TestEditMessageTextAndAnswer(t *testing.T) {
	a, srv := newAPIServer(t, response{200, `{"ok":true,"result":true}`}, response{200, `{"ok":true,"result":true}`})
	c := New(testToken, WithBaseURL(srv.URL))
	if err := c.EditMessageText(context.Background(), 1, 55, "x", &InlineKeyboardMarkup{}); err != nil {
		t.Fatal(err)
	}
	if err := c.AnswerCallbackQuery(context.Background(), "cb1", "⏳ busy"); err != nil {
		t.Fatal(err)
	}
	if a.bodies[0]["message_id"] != float64(55) || a.bodies[1]["callback_query_id"] != "cb1" || a.bodies[1]["text"] != "⏳ busy" {
		t.Errorf("bodies = %v", a.bodies)
	}
}

func TestRetry429_HonoursRetryAfterCapped(t *testing.T) {
	_, srv := newAPIServer(t,
		response{429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 120","parameters":{"retry_after":120}}`},
		response{429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`},
		response{200, `{"ok":true,"result":true}`})
	var sleeps []time.Duration
	c := New(testToken, WithBaseURL(srv.URL), WithBackoff(time.Second, 60*time.Second), recordSleeps(&sleeps))
	if err := c.AnswerCallbackQuery(context.Background(), "x", ""); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{60 * time.Second, 3 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Errorf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestRetry5xx_DoublingBackoffCapped(t *testing.T) {
	_, srv := newAPIServer(t,
		response{502, `bad gateway`}, response{500, `{"ok":false,"error_code":500,"description":"Internal"}`},
		response{503, ``}, response{502, ``}, response{200, `{"ok":true,"result":true}`})
	var sleeps []time.Duration
	c := New(testToken, WithBaseURL(srv.URL), WithBackoff(time.Second, 5*time.Second), recordSleeps(&sleeps))
	if err := c.AnswerCallbackQuery(context.Background(), "x", ""); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Errorf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestConflict_TypedAndNotRetried(t *testing.T) {
	a, srv := newAPIServer(t, response{409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`})
	c := New(testToken, WithBaseURL(srv.URL))
	_, err := c.GetUpdates(context.Background(), 0, time.Second)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if len(a.paths) != 1 {
		t.Errorf("requests = %d, want 1 (409 is not retried)", len(a.paths))
	}
}

func TestNotModified_Recognised(t *testing.T) {
	_, srv := newAPIServer(t, response{400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`})
	err := New(testToken, WithBaseURL(srv.URL)).EditMessageText(context.Background(), 1, 2, "x", nil)
	if !errors.Is(err, ErrNotModified) || errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrNotModified only", err)
	}
}

func TestBadRequest_NotRetried(t *testing.T) {
	a, srv := newAPIServer(t, response{400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`})
	err := New(testToken, WithBaseURL(srv.URL)).AnswerCallbackQuery(context.Background(), "x", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 || len(a.paths) != 1 {
		t.Errorf("err = %v, requests = %d", err, len(a.paths))
	}
}

func TestTransportError_RetriedAndNeverLeaksToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused from now on
	attempts := 0
	stop := errors.New("stop")
	c := New(testToken, WithBaseURL(url), WithSleep(func(context.Context, time.Duration) error {
		attempts++
		if attempts == 2 {
			return stop
		}
		return nil
	}))
	_, err := c.GetUpdates(context.Background(), 0, time.Second)
	if err == nil || !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the aborted retry", err)
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaks the token: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 retries before abort", attempts)
	}
}

func TestContextCancelStopsRetrying(t *testing.T) {
	_, srv := newAPIServer(t, response{502, ``})
	ctx, cancel := context.WithCancel(context.Background())
	c := New(testToken, WithBaseURL(srv.URL), WithSleep(func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}))
	if err := c.AnswerCallbackQuery(ctx, "x", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
