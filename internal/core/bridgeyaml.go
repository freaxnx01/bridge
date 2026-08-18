package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// aliasPattern is the permitted shape of a repo alias: lowercase alphanumerics
// and hyphens, not starting with a hyphen.
var aliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// bridgeConfig is the parsed shape of a repo's .bridge.yaml. Only Alias is
// consumed today; the file is intentionally extensible (future: idea-target,
// issue-labels), so unknown keys are ignored by the YAML decoder.
type bridgeConfig struct {
	Alias string `yaml:"alias"`
}

// readBridgeAlias reads and validates the alias from <repoPath>/.bridge.yaml.
// It returns the lowercased alias, or "" when the file is absent, unreadable,
// malformed, or the alias violates aliasPattern. Discovery must never fail on a
// bad .bridge.yaml, so all error paths degrade to "".
func readBridgeAlias(repoPath string) string {
	raw, err := os.ReadFile(filepath.Join(repoPath, ".bridge.yaml"))
	if err != nil {
		return ""
	}
	var cfg bridgeConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	alias := strings.ToLower(strings.TrimSpace(cfg.Alias))
	if !aliasPattern.MatchString(alias) {
		return ""
	}
	return alias
}
