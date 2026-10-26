package forge

import (
	"errors"
	"fmt"
	"net/http"
)

// HTTPError is a forge API response with an error status (≥ 400). Its text
// keeps the historical "<forge> <path>: <status>: <body>" shape.
type HTTPError struct {
	Forge      string // "github", "gitlab", "forgejo"
	Path       string
	Status     string // e.g. "401 Unauthorized"
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: %s: %s", e.Forge, e.Path, e.Status, e.Body)
}

// IsAuthError reports whether err is (or wraps) a 401 or 403 from a forge
// API — a revoked, expired or rotated token.
func IsAuthError(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.StatusCode == http.StatusUnauthorized || he.StatusCode == http.StatusForbidden)
}
