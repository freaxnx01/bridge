package store

import (
	"os"
	"path/filepath"
)

// Dir returns the bridge cache directory (~/.cache/bridge).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "bridge"), nil
}

// Path joins a name onto the cache dir.
func Path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}
