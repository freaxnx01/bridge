package core

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// LoadMRU reads newline-delimited MRU file. Most-recent-first, deduped (latest kept). Missing → empty.
func LoadMRU(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var raw []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		raw = append(raw, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		if seen[raw[i]] {
			continue
		}
		seen[raw[i]] = true
		out = append(out, raw[i])
	}
	return out, nil
}
