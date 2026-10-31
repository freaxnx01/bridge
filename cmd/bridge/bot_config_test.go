//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeBotEnvFile(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bot.env")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // umask-proof
		t.Fatal(err)
	}
	return path
}

func TestLoadBotConfig_Parses(t *testing.T) {
	path := writeBotEnvFile(t, 0o600, "# bridge bot\nexport TELEGRAM_BOT_TOKEN=\"123:abc\"\nBRIDGE_BOT_ALLOW= 111, 222 \n\nBRIDGE_BOT_API_URL='http://127.0.0.1:9'\n")
	cfg, err := loadBotConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := botConfig{Token: "123:abc", Allow: []int64{111, 222}, APIURL: "http://127.0.0.1:9"}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
}

// Review Focus: a token file readable by others is refused, and the refusal
// does not echo the token.
func TestLoadBotConfig_RefusesGroupOrWorldReadable(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		path := writeBotEnvFile(t, mode, "TELEGRAM_BOT_TOKEN=123:SECRET\nBRIDGE_BOT_ALLOW=1\n")
		_, err := loadBotConfig(path)
		if err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %04o: err = %v, want a chmod 600 refusal", mode, err)
			continue
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("mode %04o: error leaks the token: %v", mode, err)
		}
	}
}

func TestLoadBotConfig_Errors(t *testing.T) {
	cases := map[string]string{
		"no token":    "BRIDGE_BOT_ALLOW=1\n",
		"no allow":    "TELEGRAM_BOT_TOKEN=1:x\n",
		"bad allow":   "TELEGRAM_BOT_TOKEN=1:x\nBRIDGE_BOT_ALLOW=@me\n",
		"empty allow": "TELEGRAM_BOT_TOKEN=1:x\nBRIDGE_BOT_ALLOW= , \n",
	}
	for name, body := range cases {
		if _, err := loadBotConfig(writeBotEnvFile(t, 0o600, body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := loadBotConfig(filepath.Join(t.TempDir(), "missing.env")); err == nil || !strings.Contains(err.Error(), "docs/telegram-bot.md") {
		t.Errorf("missing file: err = %v", err)
	}
}

func TestDefaultBotConfigPath(t *testing.T) {
	got := defaultBotConfigPath(func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return "/x"
		}
		return ""
	})
	if got != "/x/bridge/bot.env" {
		t.Errorf("got %s", got)
	}
}
