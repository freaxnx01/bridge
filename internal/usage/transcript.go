package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one priced-able assistant turn read from a transcript.
type Turn struct {
	At     time.Time
	Model  string
	Tokens Tokens
}

// transcriptRow is the minimal shape we need out of a transcript line. Claude
// Code writes many row types; only assistant rows carry message.usage.
type transcriptRow struct {
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// maxLineBytes bounds one transcript line. Lines routinely exceed bufio's 64 KB
// default because a turn embeds tool results, and a too-small buffer would fail
// the scan on exactly the busiest sessions.
const maxLineBytes = 16 << 20

// ScanTranscripts reads every turn at or after since from the Claude Code
// transcripts under root.
//
// Two filters, both required: files whose mtime predates since are never
// opened (a file not written inside the window cannot hold a row inside it),
// and every surviving row is still checked against its own timestamp.
//
// Malformed lines are skipped rather than fatal — one corrupt line must not
// blind the whole budget rung. A missing root is the fresh-install case and
// returns no turns and no error.
func ScanTranscripts(ctx context.Context, root string, since time.Time) ([]Turn, error) {
	var out []Turn
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // raced with a delete; nothing to read
		}
		if info.ModTime().Before(since) {
			return nil
		}
		turns, err := scanFile(path, since)
		if err != nil {
			return fmt.Errorf("scan transcript %s: %w", path, err)
		}
		out = append(out, turns...)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func scanFile(path string, since time.Time) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Turn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		var r transcriptRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // a corrupt line is skipped, never fatal
		}
		if r.Message.Usage == nil || r.Timestamp.IsZero() || r.Timestamp.Before(since) {
			continue
		}
		out = append(out, Turn{
			At:    r.Timestamp,
			Model: r.Message.Model,
			Tokens: Tokens{
				Input:      r.Message.Usage.InputTokens,
				Output:     r.Message.Usage.OutputTokens,
				CacheRead:  r.Message.Usage.CacheReadInputTokens,
				CacheWrite: r.Message.Usage.CacheCreationInputTokens,
			},
		})
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// SumWindow prices every turn in [from, to) — from inclusive, to exclusive.
func SumWindow(turns []Turn, p Pricing, from, to time.Time) float64 {
	total := 0.0
	for _, t := range turns {
		if t.At.Before(from) || !t.At.Before(to) {
			continue
		}
		total += p.CostOf(t.Model, t.Tokens)
	}
	return total
}
