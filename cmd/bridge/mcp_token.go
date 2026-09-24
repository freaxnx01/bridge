package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// utf8BOM is what Windows Notepad prepends to a file saved as "UTF-8"; left in
// place it would make the token silently mismatch every bearer it is compared to.
const utf8BOM = "\xef\xbb\xbf"

// resolveMCPToken picks the static bearer token for `bridge mcp serve`.
// Precedence, highest first: the BRIDGE_MCP_TOKEN value (envToken), the
// --token-file path (flagPath), then the default file (defaultPath). The env
// var deliberately outranks the flag so an existing env-based setup keeps
// working unchanged. An explicit --token-file that can't be read is an error;
// a missing default file is not, and yields an empty token. source reports
// which of "env", "flag" or "default" supplied the token, for logging.
func resolveMCPToken(envToken, flagPath, defaultPath string) (token, source string, err error) {
	if envToken != "" {
		return envToken, "env", nil
	}
	if flagPath != "" {
		token, err := readTokenFile(flagPath)
		return token, "flag", err
	}
	if defaultPath == "" {
		return "", "", nil
	}
	token, err = readTokenFile(defaultPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	return token, "default", err
}

// readTokenFile returns the file's content without a UTF-8 BOM and
// surrounding whitespace, failing when nothing is left.
func readTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read MCP token file: %w", err)
	}
	token := strings.TrimSpace(strings.TrimPrefix(string(raw), utf8BOM))
	if token == "" {
		return "", fmt.Errorf("MCP token file %s is empty", path)
	}
	return token, nil
}

// mcpTokenDefaultPath is $XDG_CONFIG_HOME/bridge/mcp-token, else
// ~/.config/bridge/mcp-token (on Windows: %USERPROFILE%\.config\bridge\mcp-token).
// It returns "" when no home directory can be resolved.
func mcpTokenDefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "bridge", "mcp-token")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "bridge", "mcp-token")
}
