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
left in place — safe to run repeatedly. Use --dry-run to preview.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell, _ := cmd.Flags().GetString("shell")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if shell == "" {
			shell = detectShell()
		}
		switch shell {
		case "bash":
			return initBash(cmd.OutOrStdout(), dryRun)
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
	rootCmd.AddCommand(initCmd)
}

func detectShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "bash"
}

func initBash(out io.Writer, dryRun bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rc := filepath.Join(home, ".bashrc")
	existing, _ := os.ReadFile(rc) // missing file → empty content, will be created

	var toAdd []string
	for _, line := range bashManagedLines {
		if !strings.Contains(string(existing), line.detect) {
			toAdd = append(toAdd, line.body)
		}
	}

	if len(toAdd) == 0 {
		fmt.Fprintf(out, "✓ ~/.bashrc already configured for bridge; nothing to add.\n")
		return nil
	}

	block := "\n" + bashManagedHeader + "\n" + strings.Join(toAdd, "\n") + "\n"

	if dryRun {
		fmt.Fprintf(out, "Would append to %s:\n%s", rc, block)
		return nil
	}

	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ Appended %d line(s) to %s\n", len(toAdd), rc)
	fmt.Fprintf(out, "  Run `exec bash -l` (or open a new shell) to activate.\n")
	return nil
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
