// Package hooks installs the SessionStart[clear] hook that lets Claude
// restore its display label via /rename after /clear wipes it (gap G2 /
// issue #85). The hook script is embedded so the Go binary is self-
// contained: at install time it's extracted to ~/.cache/bridge/hooks/
// and referenced from the user's settings.json by absolute path.
package hooks

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed relabel.sh
var relabelScript []byte

var installMu sync.Mutex

// EffectiveConfigDir returns the directory Claude Code uses for settings:
// $CLAUDE_CONFIG_DIR if set, else $HOME/.claude. Returns "" if neither is
// resolvable (no home dir).
func EffectiveConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// EnsureRelabel writes $configDir/bridge-label with the given label and
// idempotently merges a SessionStart[clear] hook into $configDir/settings.json
// pointing at the extracted relabel.sh. No-op when configDir or label is "".
func EnsureRelabel(configDir, label string) error {
	if configDir == "" || label == "" {
		return nil
	}
	installMu.Lock()
	defer installMu.Unlock()

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("hooks: mkdir config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "bridge-label"), []byte(label), 0o644); err != nil {
		return fmt.Errorf("hooks: write label: %w", err)
	}
	scriptPath, err := extractRelabelScript()
	if err != nil {
		return err
	}
	return mergeHook(filepath.Join(configDir, "settings.json"), scriptPath)
}

func extractRelabelScript() (string, error) {
	dir := filepath.Join(cacheDir(), "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("hooks: mkdir cache: %w", err)
	}
	p := filepath.Join(dir, "relabel.sh")
	if cur, err := os.ReadFile(p); err != nil || !bytes.Equal(cur, relabelScript) {
		if err := os.WriteFile(p, relabelScript, 0o755); err != nil {
			return "", fmt.Errorf("hooks: write script: %w", err)
		}
	}
	if err := os.Chmod(p, 0o755); err != nil {
		return "", fmt.Errorf("hooks: chmod script: %w", err)
	}
	return p, nil
}

func cacheDir() string {
	if x := os.Getenv("BRIDGE_CACHE"); x != "" {
		return x
	}
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "bridge")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "bridge")
}

func mergeHook(settingsPath, scriptPath string) error {
	cmdStr := scriptPath + " 0"

	var data map[string]any
	if raw, err := os.ReadFile(settingsPath); err == nil {
		if jerr := json.Unmarshal(raw, &data); jerr != nil {
			_ = os.Rename(settingsPath, settingsPath+".corrupt")
			data = nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("hooks: read settings: %w", err)
	}
	if data == nil {
		data = map[string]any{}
	}

	hooksMap, _ := data["hooks"].(map[string]any)
	if hooksMap == nil {
		hooksMap = map[string]any{}
		data["hooks"] = hooksMap
	}

	entries, _ := hooksMap["SessionStart"].([]any)
	if hookCmdInstalled(entries, cmdStr) {
		return nil
	}
	entries = append(entries, map[string]any{
		"matcher": "clear",
		"hooks": []any{
			map[string]any{"type": "command", "command": cmdStr},
		},
	})
	hooksMap["SessionStart"] = entries

	buf, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("hooks: marshal settings: %w", err)
	}
	return atomicWrite(settingsPath, buf, 0o644)
}

func hookCmdInstalled(entries []any, cmd string) bool {
	for _, e := range entries {
		em, _ := e.(map[string]any)
		if em == nil {
			continue
		}
		hs, _ := em["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if hm == nil {
				continue
			}
			if c, _ := hm["command"].(string); c == cmd {
				return true
			}
		}
	}
	return false
}

func atomicWrite(path string, buf []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bridge-settings-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(buf); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
