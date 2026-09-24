package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp-token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveMCPToken(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")

	tests := []struct {
		name        string
		env         string
		flagPath    string
		defaultPath string
		wantToken   string
		wantSource  string
		wantErr     string
	}{
		{
			name:        "env wins over flag and default file",
			env:         "from-env",
			flagPath:    writeTokenFile(t, "from-flag"),
			defaultPath: writeTokenFile(t, "from-default"),
			wantToken:   "from-env",
			wantSource:  "env",
		},
		{
			name:       "env set never reads a missing flag file",
			env:        "from-env",
			flagPath:   missing,
			wantToken:  "from-env",
			wantSource: "env",
		},
		{
			name:        "flag file wins over default file",
			flagPath:    writeTokenFile(t, "from-flag"),
			defaultPath: writeTokenFile(t, "from-default"),
			wantToken:   "from-flag",
			wantSource:  "flag",
		},
		{
			name:     "missing flag file is an error naming the path",
			flagPath: missing,
			wantErr:  missing,
		},
		{
			name:        "default file used when env and flag are unset",
			defaultPath: writeTokenFile(t, "from-default"),
			wantToken:   "from-default",
			wantSource:  "default",
		},
		{
			name:        "missing default file yields no token and no error",
			defaultPath: missing,
		},
		{
			name:        "empty default path yields no token and no error",
			defaultPath: "",
		},
		{
			name:       "trailing CRLF and whitespace are trimmed",
			flagPath:   writeTokenFile(t, "  secret \r\n"),
			wantToken:  "secret",
			wantSource: "flag",
		},
		{
			name:       "UTF-8 BOM is stripped",
			flagPath:   writeTokenFile(t, "\xef\xbb\xbfsecret\r\n"),
			wantToken:  "secret",
			wantSource: "flag",
		},
		{
			name:     "empty flag file is an error",
			flagPath: writeTokenFile(t, " \r\n"),
			wantErr:  "empty",
		},
		{
			name:        "empty default file is an error",
			defaultPath: writeTokenFile(t, "\xef\xbb\xbf\n"),
			wantErr:     "empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, source, err := resolveMCPToken(tt.env, tt.flagPath, tt.defaultPath)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if token != tt.wantToken || source != tt.wantSource {
				t.Fatalf("got (%q, %q), want (%q, %q)", token, source, tt.wantToken, tt.wantSource)
			}
		})
	}
}

func TestMCPTokenDefaultPath(t *testing.T) {
	t.Run("XDG_CONFIG_HOME set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		if got, want := mcpTokenDefaultPath(), filepath.Join("/xdg", "bridge", "mcp-token"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("falls back to ~/.config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		if got, want := mcpTokenDefaultPath(), filepath.Join(home, ".config", "bridge", "mcp-token"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}
