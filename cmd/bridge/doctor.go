package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agents"
)

type checkStatus int

const (
	statusPass checkStatus = iota
	statusWarn
	statusFail
)

func (s checkStatus) String() string {
	switch s {
	case statusPass:
		return "PASS"
	case statusWarn:
		return "WARN"
	case statusFail:
		return "FAIL"
	}
	return "?"
}

type checkResult struct {
	name      string
	status    checkStatus
	detail    string
	remediate string
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose shim + tab-completion setup",
	Long: `doctor inspects what bridge needs to run on this host: binary on PATH,
shim files installed, rc lines sourced, and the in-process shim sentinel.
Exit code is non-zero if any check FAILs. WARNs don't fail the run.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		results := runDoctorChecks()
		hasFail := false
		for _, r := range results {
			fmt.Fprintf(out, "  %-5s %s", r.status, r.name)
			if r.detail != "" {
				fmt.Fprintf(out, " — %s", r.detail)
			}
			fmt.Fprintln(out)
			if r.status != statusPass && r.remediate != "" {
				fmt.Fprintf(out, "        → %s\n", r.remediate)
			}
			if r.status == statusFail {
				hasFail = true
			}
		}
		if hasFail {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctorChecks() []checkResult {
	home, _ := os.UserHomeDir()
	shimDir := filepath.Join(home, ".local", "share", "bridge")
	shimFile := filepath.Join(shimDir, "bridge-shim.sh")
	metaFile := filepath.Join(shimDir, "bridge-completion-meta.sh")
	rc := filepath.Join(home, ".bashrc")
	rcBytes, _ := os.ReadFile(rc)
	rcContent := string(rcBytes)

	var out []checkResult

	// 1. binary on PATH
	if path, err := exec.LookPath("bridge"); err == nil {
		out = append(out, checkResult{
			name:   "bridge on PATH",
			status: statusPass,
			detail: path,
		})
	} else {
		out = append(out, checkResult{
			name:      "bridge on PATH",
			status:    statusFail,
			detail:    "not found",
			remediate: "run `make install` from the bridge repo",
		})
	}

	// 2. shim file
	out = append(out, fileCheck("shim file", shimFile, "run `make install-shim`"))
	// 3. augmenter file
	out = append(out, fileCheck("meta-augmenter file", metaFile, "run `make install-shim`"))

	// 4-6. rc lines
	out = append(out, rcLineCheck("rc: shim source", rcContent, "bridge-shim.sh"))
	out = append(out, rcLineCheck("rc: completion source", rcContent, "bridge completion bash"))
	out = append(out, rcLineCheck("rc: meta-augmenter source", rcContent, "bridge-completion-meta.sh"))

	// 7. shim loaded in current shell
	if os.Getenv("BRIDGE_SHIM_LOADED") == "1" {
		out = append(out, checkResult{
			name:   "shim loaded in current shell",
			status: statusPass,
		})
	} else {
		out = append(out, checkResult{
			name:      "shim loaded in current shell",
			status:    statusWarn,
			detail:    "BRIDGE_SHIM_LOADED not set",
			remediate: "open a new terminal or `source ~/.bashrc`",
		})
	}

	// 8. bash-completion package
	bcPaths := []string{
		"/usr/share/bash-completion/bash_completion",
		"/etc/bash_completion",
		"/usr/local/share/bash-completion/bash_completion", // homebrew
	}
	bcFound := ""
	for _, p := range bcPaths {
		if _, err := os.Stat(p); err == nil {
			bcFound = p
			break
		}
	}
	if bcFound != "" {
		out = append(out, checkResult{
			name:   "bash-completion package",
			status: statusPass,
			detail: bcFound,
		})
	} else {
		out = append(out, checkResult{
			name:      "bash-completion package",
			status:    statusWarn,
			detail:    "not found at standard paths",
			remediate: "install OS package `bash-completion` (debian/ubuntu) or `brew install bash-completion@2`",
		})
	}

	// 9. repos root walkable
	root := reposRoot()
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		out = append(out, checkResult{
			name:   "repos root",
			status: statusPass,
			detail: root,
		})
	} else {
		out = append(out, checkResult{
			name:      "repos root",
			status:    statusFail,
			detail:    fmt.Sprintf("%s: %v", root, err),
			remediate: "create the directory or set BRIDGE_REPOS_ROOT",
		})
	}

	// 10. repo-meta.json cache (optional — warn only)
	metaCache := filepath.Join(cacheRoot(), "repo-meta.json")
	if _, err := os.Stat(metaCache); err == nil {
		out = append(out, checkResult{
			name:   "repo-meta.json cache",
			status: statusPass,
			detail: metaCache,
		})
	} else {
		out = append(out, checkResult{
			name:      "repo-meta.json cache",
			status:    statusWarn,
			detail:    "not present — meta-keyword TAB fallback has no data",
			remediate: "run `bridge sync` (or similar) to populate; basename completion still works",
		})
	}

	// 11. Alias completions wired via `complete -F __start_bridge <name>`.
	// Informational only — every host's alias set is personal. Lists the
	// names found; "(none)" prompts the user that `bridge init --alias`
	// exists. The cobra-installed `bridge` registration is excluded.
	aliases := scanAliasCompletions(rcContent)
	{
		detail := strings.Join(aliases, ", ")
		if detail == "" {
			detail = "(none — wire with `bridge init --alias=br,brg`)"
		}
		out = append(out, checkResult{
			name:   "alias completions",
			status: statusPass,
			detail: detail,
		})
	}

	// 12. BRIDGE_DEFAULT_AGENT (auto-launch on `bridge <repo>`)
	agent := os.Getenv("BRIDGE_DEFAULT_AGENT")
	args := os.Getenv("BRIDGE_DEFAULT_AGENT_ARGS")
	switch {
	case agent == "":
		out = append(out, checkResult{
			name:      "BRIDGE_DEFAULT_AGENT (auto-launch)",
			status:    statusWarn,
			detail:    "unset — `bridge <repo>` will only cd, no agent launch",
			remediate: "run `bridge init --agent=claude --agent-args=\"--remote-control --dangerously-skip-permissions\"`",
		})
	default:
		if _, err := agents.Resolve(agent); err != nil {
			out = append(out, checkResult{
				name:      "BRIDGE_DEFAULT_AGENT",
				status:    statusFail,
				detail:    fmt.Sprintf("%q: %v", agent, err),
				remediate: "use one of: claude, copilot, opencode, code",
			})
		} else {
			detail := agent
			if args != "" {
				detail += " (args: " + args + ")"
			}
			out = append(out, checkResult{
				name:   "BRIDGE_DEFAULT_AGENT (auto-launch)",
				status: statusPass,
				detail: detail,
			})
		}
	}

	return out
}

func fileCheck(name, path, remediate string) checkResult {
	if _, err := os.Stat(path); err == nil {
		return checkResult{name: name, status: statusPass, detail: path}
	}
	return checkResult{
		name:      name,
		status:    statusFail,
		detail:    path + " missing",
		remediate: remediate,
	}
}

// scanAliasCompletions returns the alias names found in lines like
// `complete -o default -F __start_bridge <name>`. The cobra-default
// `bridge` registration is filtered out so only user-wired aliases show
// up. Names containing non-identifier characters are skipped defensively.
func scanAliasCompletions(rcContent string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(rcContent, "\n") {
		idx := strings.Index(line, "__start_bridge")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len("__start_bridge"):])
		if rest == "" {
			continue
		}
		// Take the first whitespace-separated token.
		name := strings.Fields(rest)[0]
		if name == "bridge" || seen[name] {
			continue
		}
		// Defensive: only emit names that look like a shell identifier.
		if !isShellIdent(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func isShellIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func rcLineCheck(name, rc, detect string) checkResult {
	if strings.Contains(rc, detect) {
		return checkResult{name: name, status: statusPass}
	}
	return checkResult{
		name:      name,
		status:    statusFail,
		detail:    "not found in ~/.bashrc",
		remediate: "run `bridge init` to add missing source lines",
	}
}
