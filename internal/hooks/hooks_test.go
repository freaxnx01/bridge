package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupEnv(t *testing.T) (configDir, cache string) {
	t.Helper()
	tmp := t.TempDir()
	configDir = filepath.Join(tmp, "claude")
	cache = filepath.Join(tmp, "cache")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("BRIDGE_CACHE", cache)
	t.Setenv("XDG_CACHE_HOME", "")
	return
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func sessionStartCommands(t *testing.T, settings map[string]any) []string {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	entries, _ := hooks["SessionStart"].([]any)
	var out []string
	for _, e := range entries {
		em, _ := e.(map[string]any)
		hs, _ := em["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd != "" {
				out = append(out, cmd)
			}
		}
	}
	return out
}

func TestEffectiveConfigDirEnvWins(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/explicit/path")
	if got := EffectiveConfigDir(); got != "/explicit/path" {
		t.Errorf("got %q", got)
	}
}

func TestEnsureRelabel_FreshInstall(t *testing.T) {
	cfg, _ := setupEnv(t)
	if err := EnsureRelabel(cfg, "myrepo [feat]"); err != nil {
		t.Fatalf("EnsureRelabel: %v", err)
	}

	// Label file.
	label, err := os.ReadFile(filepath.Join(cfg, "bridge-label"))
	if err != nil {
		t.Fatalf("read label: %v", err)
	}
	if string(label) != "myrepo [feat]" {
		t.Errorf("label content: %q", label)
	}

	// Settings entry.
	settings := readSettings(t, filepath.Join(cfg, "settings.json"))
	cmds := sessionStartCommands(t, settings)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 SessionStart command, got %d: %v", len(cmds), cmds)
	}
	if !bytes.HasSuffix([]byte(cmds[0]), []byte("/relabel.sh 0")) {
		t.Errorf("unexpected command: %q", cmds[0])
	}

	// Script extracted and matches embed.
	scriptPath := filepath.Join(cacheDir(), "hooks", "relabel.sh")
	got, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !bytes.Equal(got, relabelScript) {
		t.Error("extracted script does not match embedded content")
	}
	fi, err := os.Stat(scriptPath)
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("script not executable: mode=%v err=%v", fi.Mode(), err)
	}
}

func TestEnsureRelabel_Idempotent(t *testing.T) {
	cfg, _ := setupEnv(t)
	for i := 0; i < 3; i++ {
		if err := EnsureRelabel(cfg, "myrepo"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	settings := readSettings(t, filepath.Join(cfg, "settings.json"))
	cmds := sessionStartCommands(t, settings)
	if len(cmds) != 1 {
		t.Errorf("expected 1 entry after 3 calls, got %d: %v", len(cmds), cmds)
	}
}

func TestEnsureRelabel_PreservesExistingHooks(t *testing.T) {
	cfg, _ := setupEnv(t)
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{
		"hooks": map[string]any{
			"Notification": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/existing/notify.sh"},
					},
				},
			},
			"SessionStart": []any{
				map[string]any{
					"matcher": "clear",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/some/other/relabel.sh 7"},
					},
				},
			},
		},
		"misc": "keepme",
	}
	raw, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRelabel(cfg, "myrepo"); err != nil {
		t.Fatalf("EnsureRelabel: %v", err)
	}
	settings := readSettings(t, filepath.Join(cfg, "settings.json"))

	// Existing top-level key kept.
	if settings["misc"] != "keepme" {
		t.Errorf("clobbered top-level key")
	}
	// Existing Notification entry kept.
	hooks, _ := settings["hooks"].(map[string]any)
	notif, _ := hooks["Notification"].([]any)
	if len(notif) != 1 {
		t.Errorf("Notification entries lost: %v", notif)
	}
	// SessionStart now has the existing-foreign command AND ours.
	cmds := sessionStartCommands(t, settings)
	if len(cmds) != 2 {
		t.Fatalf("expected 2 SessionStart entries, got %d: %v", len(cmds), cmds)
	}
}

func TestEnsureRelabel_CorruptSettingsBackedUp(t *testing.T) {
	cfg, _ := setupEnv(t)
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(cfg, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRelabel(cfg, "myrepo"); err != nil {
		t.Fatalf("EnsureRelabel: %v", err)
	}
	if _, err := os.Stat(settingsPath + ".corrupt"); err != nil {
		t.Errorf("expected backup at %s.corrupt", settingsPath)
	}
	// New settings should be valid JSON with our entry.
	cmds := sessionStartCommands(t, readSettings(t, settingsPath))
	if len(cmds) != 1 {
		t.Errorf("expected 1 entry after recovery, got %d: %v", len(cmds), cmds)
	}
}

func TestEnsureRelabel_EmptyArgsNoOp(t *testing.T) {
	cfg, _ := setupEnv(t)
	if err := EnsureRelabel("", "lbl"); err != nil {
		t.Errorf("empty configDir should be no-op, got %v", err)
	}
	if err := EnsureRelabel(cfg, ""); err != nil {
		t.Errorf("empty label should be no-op, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json should not have been created, err=%v", err)
	}
}

func TestEmbeddedScriptMatchesPublicCopy(t *testing.T) {
	// Keep internal/hooks/relabel.sh (the embedded source) and
	// bridge-hooks/relabel.sh (the on-disk copy that users' existing
	// settings.json may reference) byte-identical so they don't drift.
	pub, err := os.ReadFile(filepath.Join("..", "..", "bridge-hooks", "relabel.sh"))
	if err != nil {
		t.Fatalf("read bridge-hooks/relabel.sh: %v", err)
	}
	if !bytes.Equal(pub, relabelScript) {
		t.Fatal("internal/hooks/relabel.sh and bridge-hooks/relabel.sh have diverged — keep them in sync")
	}
}
