package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RESTHandler serves the file toolset over plain HTTP at POST /api/tools/{name}.
//
// It lives in this package on purpose: the input structs and handler methods are
// unexported, and sharing them is what stops the REST and MCP surfaces drifting.
// There is one implementation of each tool; this is a second transport, not a
// second copy.
func RESTHandler(deps Deps) http.Handler {
	return &restHandler{deps: deps}
}

type restHandler struct{ deps Deps }

func (h *restHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		restError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	switch strings.TrimPrefix(r.URL.Path, "/api/tools/") {
	case "read_file":
		restCall(w, r, h.deps.handleReadFile)
	case "list_tree":
		restCall(w, r, h.deps.handleListTree)
	case "search_code":
		restCall(w, r, h.deps.handleSearchCode)
	case "put_file":
		// MCP enforces this by not registering the tool on a read-only server
		// (internal/mcp/server.go:82-93); REST has no registration step, so
		// the check is explicit here. The path allowlist, confirm=true draft
		// gate and sha-required-on-update check all live inside handlePutFile —
		// reimplementing any of them here is how the two transports drift.
		if h.deps.ReadOnly {
			restError(w, http.StatusForbidden, "write tools are disabled on this server")
			return
		}
		restCall(w, r, h.deps.handlePutFile)
	default:
		restError(w, http.StatusNotFound, "unknown tool")
	}
}

// restCall decodes In from the body, invokes the shared handler, and encodes Out.
// The *mcp.CallToolResult the handler returns is MCP presentation and is discarded.
func restCall[In any, Out any](
	w http.ResponseWriter,
	r *http.Request,
	handle func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) {
	var in In
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		restError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	_, out, err := handle(r.Context(), nil, in)
	if err != nil {
		restError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func restError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
