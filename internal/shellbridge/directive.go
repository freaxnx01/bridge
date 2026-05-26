// Package shellbridge encodes the directive protocol between the Go binary
// and the parent shell shim. The binary writes exactly one directive line
// to stdout via __preflight; the shim parses it and changes the parent
// shell's state accordingly.
package shellbridge

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// EmitCD writes "cd:<path>\n".
func EmitCD(w io.Writer, path string) error {
	if path == "" {
		return errors.New("cd directive requires non-empty path")
	}
	_, err := fmt.Fprintf(w, "cd:%s\n", path)
	return err
}

// EmitExec writes "exec:<shell-quoted argv>\n". The shim does `exec ${argv}`,
// so arguments containing whitespace must be quoted.
func EmitExec(w io.Writer, argv []string) error {
	if len(argv) == 0 {
		return errors.New("exec directive requires non-empty argv")
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	_, err := fmt.Fprintf(w, "exec:%s\n", strings.Join(quoted, " "))
	return err
}

// EmitNoop writes "noop\n".
func EmitNoop(w io.Writer) error {
	_, err := fmt.Fprintln(w, "noop")
	return err
}

// shellQuote returns s safely quoted for /bin/sh.
// Only quotes when needed; arguments without whitespace or shell metacharacters
// are passed through unchanged.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '+' || r == '=' || r == ',') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
