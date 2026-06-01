package nav

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run launches the nav TUI. With cfg.Once it renders one frame and returns
// (smoke path). Otherwise it requires an interactive TTY; without one it prints
// a notice and returns nil rather than starting the program.
func Run(cfg Config) error {
	if cfg.Once {
		return runOnce(cfg, os.Stdout)
	}
	if !isInteractive() {
		fmt.Fprintln(os.Stderr, "bridge nav: needs an interactive terminal (tmux attach is unavailable here)")
		return nil
	}
	if cfg.DebugKeys != "" {
		fmt.Fprintf(os.Stderr, "bridge nav: logging keys to %s\n", cfg.DebugKeys)
	}
	p := tea.NewProgram(initialModel(cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func runOnce(cfg Config, w io.Writer) error {
	m := initialModel(cfg)
	m.width, m.height = 130, 42
	// Resolve the synchronous loaders so the frame has content.
	if msg := loadLocalReposCmd(cfg.ReposRoots)(); msg != nil {
		mm, _ := m.Update(msg)
		m = mm.(Model)
	}
	fmt.Fprintln(w, m.View())
	return nil
}

// isInteractive reports whether stdin is a character device (a terminal).
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
