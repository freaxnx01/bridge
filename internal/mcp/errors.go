package mcp

import "errors"

// ErrInvalidInput marks an error caused by the caller's input rather than by a
// failure inside the server. The MCP transport does not distinguish the two —
// every tool error is reported the same way — but a REST caller needs to tell
// its own mistake from a server fault, so restCall maps this to 400.
//
// Wrapping preserves the original message verbatim: Error() delegates, so no
// existing error text or MCP behaviour changes.
var ErrInvalidInput = errors.New("invalid input")

type invalidInputError struct{ err error }

func (e invalidInputError) Error() string        { return e.err.Error() }
func (e invalidInputError) Unwrap() error        { return e.err }
func (e invalidInputError) Is(target error) bool { return target == ErrInvalidInput }

// invalidInput marks err as caller-caused.
func invalidInput(err error) error { return invalidInputError{err: err} }
