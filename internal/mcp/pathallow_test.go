package mcp

import "testing"

func TestPathAllowlist_Allows(t *testing.T) {
	tests := []struct {
		name string
		list PathAllowlist
		path string
		want bool
	}{
		{"docs-nested", PathAllowlist{"docs/**"}, "docs/ai-notes/x.md", true},
		{"docs-top-level-file", PathAllowlist{"docs/**"}, "docs/x.md", true},
		{"docs-dir-itself-not-matched-without-trailing-content", PathAllowlist{"docs/**"}, "docs", true},
		{"root-md-allowed", PathAllowlist{"*.md"}, "README.md", true},
		{"root-md-wrong-ext", PathAllowlist{"*.md"}, "README.txt", false},
		{"nested-md-not-matched-by-root-glob", PathAllowlist{"*.md"}, "docs/README.md", false},
		{"outside-allowlist-rejected", PathAllowlist{"docs/**", "*.md"}, "internal/mcp/tools.go", false},
		{"similarly-prefixed-dir-not-matched", PathAllowlist{"docs/**"}, "docs-legacy/x.md", false},
		{"github-denied-even-with-matching-allowlist-entry", PathAllowlist{".github/**"}, ".github/workflows/ci.yml", false},
		{"github-exact-file-denied", PathAllowlist{"*"}, ".github", false},
		{"empty-allowlist-denies-everything", PathAllowlist{}, "docs/x.md", false},
		{"path-traversal-into-github-denied", PathAllowlist{"docs/**"}, "docs/../.github/workflows/evil.yml", false},
		{"path-traversal-out-of-repo-denied", PathAllowlist{"docs/**"}, "docs/../../etc/passwd", false},
		{"absolute-path-denied", PathAllowlist{"docs/**"}, "/etc/passwd", false},
		{"dotdot-root-traversal-denied", PathAllowlist{"*.md"}, "../README.md", false},
		{"query-metachar-denied", PathAllowlist{"*.md"}, "justfile?x.md", false},
		{"query-metachar-on-dotfile-denied", PathAllowlist{"*.md"}, ".envrc?x.md", false},
		{"fragment-metachar-denied", PathAllowlist{"docs/**/*.md"}, "docs/a.md#x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.list.Allows(tt.path); got != tt.want {
				t.Errorf("%v.Allows(%q) = %v, want %v", tt.list, tt.path, got, tt.want)
			}
		})
	}
}

func TestDefaultPathAllowlist(t *testing.T) {
	if !DefaultPathAllowlist.Allows("docs/x.md") {
		t.Error("default allowlist must allow docs/**")
	}
	if !DefaultPathAllowlist.Allows("README.md") {
		t.Error("default allowlist must allow root-level *.md")
	}
	if DefaultPathAllowlist.Allows("docs/x.go") {
		t.Error("default allowlist must not allow non-md files under docs/")
	}
	if DefaultPathAllowlist.Allows(".github/workflows/ci.yml") {
		t.Error("default allowlist must never allow .github/**")
	}
}
