// internal/web/server.go
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var staticFiles embed.FS

// Server wires the SSE Hub, API handlers, and embedded Svelte SPA.
type Server struct {
	hub     *Hub
	handler http.Handler
}

// NewServer creates a Server that routes:
//   - /api/events  → SSE hub (handled in-package; avoids circular import with internal/api)
//   - /api/*       → apiMux (registered by cmd/bridge/serve.go)
//   - /            → embedded Svelte SPA with client-side routing fallback
func NewServer(hub *Hub, apiMux *http.ServeMux) *Server {
	mux := http.NewServeMux()

	// SSE handled here to avoid circular import between internal/web and internal/api
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		serveEvents(hub, w, r)
	})

	// Delegate all other /api/ routes to the caller-supplied mux
	mux.Handle("/api/", apiMux)

	// SPA: embedded Svelte assets with fallback to index.html for client-side routing
	dist, err := fs.Sub(staticFiles, "dist")
	if err != nil {
		// unreachable: //go:embed dist guarantees "dist" exists in staticFiles at compile time
		panic(fmt.Sprintf("internal/web: embedded dist subdir missing: %v", err))
	}
	mux.Handle("/", spaHandler(dist))

	return &Server{hub: hub, handler: mux}
}

// Handler returns the http.Handler for use with http.Server.
func (s *Server) Handler() http.Handler { return s.handler }

// Hub exposes the SSE hub for broadcasting events from cmd/bridge/serve.go.
func (s *Server) Hub() *Hub { return s.hub }

// serveEvents upgrades the connection to an SSE stream and blocks until the
// client disconnects or the request context is cancelled.
func serveEvents(hub *Hub, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			w.Write(msg) //nolint:errcheck // best-effort; client disconnect is benign
			flusher.Flush()
		}
	}
}

// spaHandler serves files from dist; falls back to index.html for paths that
// don't match a file (enables Svelte client-side routing).
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := dist.Open(path)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path: serve index.html so Svelte router handles it
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
