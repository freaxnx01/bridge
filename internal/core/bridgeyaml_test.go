package core

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBridgeYAML(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".bridge.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadBridgeAlias(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		write    bool
		want     string
	}{
		{"valid", "alias: br\n", true, "br"},
		{"valid_with_comment", "# repo alias\nalias: agp\n", true, "agp"},
		{"uppercase_lowercased", "alias: BR\n", true, "br"},
		{"quoted", "alias: \"ainstr\"\n", true, "ainstr"},
		{"hyphen_and_digits", "alias: web-2\n", true, "web-2"},
		{"missing_file", "", false, ""},
		{"empty_alias", "alias: \"\"\n", true, ""},
		{"malformed_yaml", "alias: [not a scalar\n", true, ""},
		{"invalid_leading_hyphen", "alias: -bad\n", true, ""},
		{"invalid_chars", "alias: bad_slug!\n", true, ""},
		{"no_alias_key", "other: value\n", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.write {
				writeBridgeYAML(t, dir, tc.contents)
			}
			if got := readBridgeAlias(dir); got != tc.want {
				t.Fatalf("readBridgeAlias() = %q, want %q", got, tc.want)
			}
		})
	}
}
