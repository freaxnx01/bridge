package oauth

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Server exposes bridge's authorization-server endpoints. It is constructed by
// cmd/bridge and mounted outside the bearer middleware: a client cannot present
// a token it has not yet obtained.
type Server struct {
	cfg        Config
	store      *Store
	httpClient *http.Client
	authentik  *authentikEndpoints

	sessMu   sync.Mutex
	sessions map[string]*loginSession
}

// NewServer returns a Server backed by store.
func NewServer(cfg Config, store *Store) *Server {
	return &Server{
		cfg:        cfg,
		store:      store,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		sessions:   map[string]*loginSession{},
	}
}

// Handler returns the mux for the OAuth subtree and metadata documents.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleAuthServerMetadata)
	mux.HandleFunc("POST /oauth/register", s.handleRegister)
	mux.HandleFunc("GET /oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("GET /oauth/callback", s.handleCallback)
	mux.HandleFunc("POST /oauth/token", s.handleToken)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // response already committed; nothing actionable remains
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
