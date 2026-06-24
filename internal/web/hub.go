// internal/web/hub.go
package web

import (
	"context"
	"encoding/json"
)

// Event is an SSE event broadcast to all connected clients.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Hub manages SSE client channels. Call Run in a goroutine before Subscribe.
type Hub struct {
	broadcast  chan Event
	register   chan chan []byte
	unregister chan chan []byte
	clients    map[chan []byte]struct{}
}

// NewHub returns a Hub ready to Run.
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan Event, 16),
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
		clients:    make(map[chan []byte]struct{}),
	}
}

// Run processes hub events until ctx is cancelled. Call in a dedicated goroutine.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ch := <-h.register:
			h.clients[ch] = struct{}{}
		case ch := <-h.unregister:
			delete(h.clients, ch)
			close(ch)
		case ev := <-h.broadcast:
			b, _ := json.Marshal(ev)
			msg := append([]byte("data: "), b...)
			msg = append(msg, '\n', '\n')
			for ch := range h.clients {
				select {
				case ch <- msg:
				default: // slow client; drop rather than block
				}
			}
		}
	}
}

// Broadcast sends ev to all connected clients. Non-blocking; drops if the
// internal channel is full (capacity 16).
func (h *Hub) Broadcast(ev Event) {
	select {
	case h.broadcast <- ev:
	default:
	}
}

// Subscribe registers a new SSE client and returns its receive channel.
// The channel is closed by the hub when Unsubscribe is called.
func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	h.register <- ch
	return ch
}

// Unsubscribe removes the client and closes its channel.
func (h *Hub) Unsubscribe(ch chan []byte) {
	h.unregister <- ch
}
