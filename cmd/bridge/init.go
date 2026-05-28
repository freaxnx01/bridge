package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// bashManagedLines are the source lines `bridge init` writes to ~/.bashrc.
// Each entry has a substring used to detect prior presence (so the writer
// can skip lines that already exist) and the literal line(s) to append.
// Order matters: shim must source before completion (the cobra script
// registers `complete -F __start_bridge bridge`; the augmenter then wraps
// __start_bridge, so the augmenter line must come last).
var bashManagedLines = []struct {
	detect string
	body   string
}{
	{
		detect: "bridge-shim.sh",
		body:   `_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f`,
	},
	{
		detect: "bridge completion bash",
		body:   `command -v bridge >/dev/null && source <(bridge completion bash)`,
	},
	{
		detect: "bridge-completion-meta.sh",
		body: `[ -f ~/.local/share/bridge/bridge-completion-meta.sh ] && \
    source ~/.local/share/bridge/bridge-completion-meta.sh`,
	},
}

const bashManagedHeader = "# bridge — repo picker + tab-completion (managed by `bridge init`)"

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Wire shim + tab-completion into your shell rc file (idempotent)",
	Long: `init appends the source lines bridge needs into your shell rc file
(~/.bashrc for bash, $PROFILE for powershell). Lines already present are
left in place — safe to run repeatedly. Use --dry-run to preview.

Pass --agent and --agent-args to also write BRIDGE_DEFAULT_AGENT and
BRIDGE_DEFAULT_AGENT_ARGS exports so ` + "`bridge <repo>`" + ` auto-launches the
configured agent. Existing export lines are replaced in place; pass an
empty value to leave them untouched.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell, _ := cmd.Flags().GetString("shell")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		agent, _ := cmd.Flags().GetString("agent")
		agentArgs, _ := cmd.Flags().GetString("agent-args")
		aliases, _ := cmd.Flags().GetString("alias")
		if shell == "" {
			shell = detectShell()
		}
		switch shell {
		case "bash":
			return initBash(cmd.OutOrStdout(), dryRun, agent, agentArgs, aliases)
		case "powershell", "pwsh":
			return initPowerShell(cmd.OutOrStdout(), dryRun)
		default:
			return fmt.Errorf("unsupported shell %q (use bash or powershell)", shell)
		}
	},
}

func init() {
	initCmd.Flags().String("shell", "", "shell to configure (bash, powershell); default: auto-detect")
	initCmd.Flags().Bool("dry-run", false, "print what would change without modifying files")
	initCmd.Flags().String("agent", "", "set BRIDGE_DEFAULT_AGENT (e.g. claude); enables auto-launch on `bridge <repo>`")
	initCmd.Flags().String("agent-args", "", "set BRIDGE_DEFAULT_AGENT_ARGS (e.g. \"--remote-control --dangerously-skip-permissions\")")
	initCmd.Flags().String("alias", "", "comma-separated shell aliases/functions to wire to bridge completion (e.g. br,brg)")
	rootCmd.AddCommand(initCmd)
}

func detectShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "bash"
}

func initBash(out io.Writer, dryRun bool, agent, agentArgs, aliases string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rc := filepath.Join(home, ".bashrc")
	existing, _ := os.ReadFile(rc) // missing file → empty content, will be created
	content := string(existing)

	// Source lines (shim/completion/augmenter) — append-if-missing block.
	var toAdd []string
	for _, line := range bashManagedLines {
		if !strings.Contains(content, line.detect) {
			toAdd = append(toAdd, line.body)
		}
	}
	// Alias completion lines. Each adds a guarded `complete -F __start_bridge
	// <name>` line so the alias inherits bridge's tab-completion (cobra binds
	// `complete -F __start_bridge bridge` only). The declare-F guard makes
	// the line a no-op when completion didn't load (e.g. binary missing).
	for _, name := range parseAliases(aliases) {
		line := "complete -o default -o nospace -F __start_bridge " + name
		// Whole-line match (binding terminated by newline or end-of-file) so a
		// shorter alias like "br" isn't falsely detected inside a longer "brg"
		// binding via a bare substring search.
		if !strings.Contains(content, line+"\n") && !strings.HasSuffix(content, line) {
			toAdd = append(toAdd, "declare -F __start_bridge >/dev/null && \\\n    "+line)
		}
	}
	var sourceBlock string
	if len(toAdd) > 0 {
		sourceBlock = "\n" + bashManagedHeader + "\n" + strings.Join(toAdd, "\n") + "\n"
	}

	// Export lines (BRIDGE_DEFAULT_AGENT*) — replace-in-place semantics, so
	// `bridge init --agent=opencode` after `--agent=claude` actually swaps
	// the value rather than leaving the stale line.
	exports := []struct{ name, value string }{
		{"BRIDGE_DEFAULT_AGENT", agent},
		{"BRIDGE_DEFAULT_AGENT_ARGS", agentArgs},
	}
	exportSummary := []string{}
	for _, e := range exports {
		if e.value == "" {
			continue
		}
		var changed bool
		content, changed = upsertExport(content, e.name, e.value)
		if changed {
			exportSummary = append(exportSummary, fmt.Sprintf(`export %s=%s`, e.name, shellQuote(e.value)))
		}
	}

	if sourceBlock == "" && len(exportSummary) == 0 {
		fmt.Fprintf(out, "✓ ~/.bashrc already configured for bridge; nothing to add.\n")
		return nil
	}

	finalContent := content + sourceBlock

	if dryRun {
		if sourceBlock != "" {
			fmt.Fprintf(out, "Would append to %s:\n%s", rc, sourceBlock)
		}
		for _, l := range exportSummary {
			fmt.Fprintf(out, "Would set in %s: %s\n", rc, l)
		}
		return nil
	}

	if err := os.WriteFile(rc, []byte(finalContent), 0o644); err != nil {
		return err
	}
	added := 0
	if sourceBlock != "" {
		added = len(toAdd)
	}
	fmt.Fprintf(out, "✓ Updated %s (%d source line(s) added, %d export(s) set)\n", rc, added, len(exportSummary))
	if added > 0 || len(exportSummary) > 0 {
		fmt.Fprintf(out, "  Run `exec bash -l` (or open a new shell) to activate.\n")
	}
	return nil
}

// upsertExport replaces an existing `export NAME=...` line in content (any
// quoting), or appends a fresh one. Returns (newContent, changedFromInput).
// Idempotent when the existing value already matches.
func upsertExport(content, name, value string) (string, bool) {
	target := fmt.Sprintf(`export %s=%s`, name, shellQuote(value))
	prefix := "export " + name + "="
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), prefix) {
			if l == target {
				return content, false
			}
			lines[i] = target
			return strings.Join(lines, "\n"), true
		}
	}
	// Append at end of content (caller may add its own block separator).
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + target + "\n", true
}

// parseAliases splits a comma-separated alias list and trims each name.
// Empty entries are dropped. Returns nil for an empty input.
func parseAliases(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, name := range parts {
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// shellQuote returns a POSIX-safe representation of value for a bash export
// line. Strings without metachars are returned as-is; otherwise wrapped in
// double quotes with embedded `"`, `$`, `\`, and `` ` `` escaped.
func shellQuote(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"'$`\\\n") {
		return value
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '$', '`', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// initPowerShell writes the equivalent lines to $PROFILE on Windows, or
// prints them to stdout on other platforms so the user can paste them on
// the Windows side.
func initPowerShell(out io.Writer, dryRun bool) error {
	lines := []string{
		`# bridge — repo picker + tab-completion (managed by ` + "`bridge init`" + `)`,
		`. "$env:LOCALAPPDATA\bridge\bridge-shim.ps1"`,
		`bridge.exe completion powershell | Out-String | Invoke-Expression`,
	}
	block := strings.Join(lines, "\n") + "\n"

	if runtime.GOOS != "windows" {
		fmt.Fprintf(out, "Paste the following into your PowerShell $PROFILE on Windows:\n\n%s", block)
		return nil
	}

	profile := os.Getenv("PROFILE")
	if profile == "" {
		fmt.Fprintf(out, "$PROFILE not set; paste the following manually:\n\n%s", block)
		return nil
	}

	existing, _ := os.ReadFile(profile)
	missing := []string{}
	for _, l := range lines[1:] { // skip the comment header for presence check
		marker := l
		if !strings.Contains(string(existing), strings.TrimSpace(strings.SplitN(marker, " ", 2)[0])) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintf(out, "✓ $PROFILE already configured; nothing to add.\n")
		return nil
	}
	toWrite := "\n" + lines[0] + "\n" + strings.Join(missing, "\n") + "\n"
	if dryRun {
		fmt.Fprintf(out, "Would append to %s:\n%s", profile, toWrite)
		return nil
	}
	f, err := os.OpenFile(profile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(toWrite); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ Appended to %s\n", profile)
	return nil
}
