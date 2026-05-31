package nav

import (
	"strings"
	"testing"
)

func TestRun_Once_RendersFrameNoTTY(t *testing.T) {
	// --once must produce output without a TTY (CI smoke path).
	var sb strings.Builder
	err := runOnce(Config{ReposRoots: []string{t.TempDir()}}, &sb)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !strings.Contains(sb.String(), "filter:") {
		t.Errorf("once frame missing picker content:\n%s", sb.String())
	}
}
