// internal/web/hub_test.go
package web

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestHub_BroadcastReachesAllClients(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()

	hub.Broadcast(Event{Type: "repo-updated", Data: map[string]string{"name": "foo"}})

	for _, ch := range []chan []byte{ch1, ch2} {
		select {
		case msg := <-ch:
			if !bytes.Contains(msg, []byte("repo-updated")) {
				t.Errorf("event missing type: %s", msg)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event")
		}
	}
}

func TestHub_UnsubscribeClosesChannel(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	ch := hub.Subscribe()
	hub.Unsubscribe(ch)

	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after Unsubscribe")
	}
}

func TestHub_BroadcastIsSSEFormat(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	ch := hub.Subscribe()
	hub.Broadcast(Event{Type: "test"})

	select {
	case msg := <-ch:
		if !bytes.HasPrefix(msg, []byte("data: ")) {
			t.Errorf("SSE message must start with 'data: ', got: %s", msg)
		}
		if !bytes.HasSuffix(msg, []byte("\n\n")) {
			t.Errorf("SSE message must end with double newline, got: %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
