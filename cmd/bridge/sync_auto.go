package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/store"
)

var (
	syncAuto     bool
	syncInterval time.Duration
)

func init() {
	syncCmd.Flags().BoolVar(&syncAuto, "auto", false, "run in a loop until stopped")
	syncCmd.Flags().DurationVar(&syncInterval, "interval", 5*time.Minute, "interval between syncs in --auto mode")
	// Wrap the existing RunE so --auto takes precedence.
	prev := syncCmd.RunE
	syncCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if syncAuto {
			return runSyncAuto(cmd)
		}
		return prev(cmd, args)
	}
}

func runSyncAuto(cmd *cobra.Command) error {
	pidPath := filepath.Join(cacheRoot(), "sync.pid")
	if existing, _ := store.ReadPIDFile(pidPath); existing > 0 && store.IsPIDRunning(existing) {
		return fmt.Errorf("sync --auto already running (PID %d)", existing)
	}
	if err := store.WritePIDFile(pidPath, os.Getpid()); err != nil {
		return err
	}
	defer store.RemovePIDFile(pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("sync auto: signal received, stopping")
		cancel()
	}()

	maxIter := 0
	if v := os.Getenv("BRIDGE_DAEMON_MAX_ITERATIONS"); v != "" {
		n, _ := strconv.Atoi(v)
		maxIter = n
	}

	iter := 0
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		if err := runSyncNow(cmd); err != nil {
			slog.Warn("sync iter failed", "err", err)
		}
		iter++
		if maxIter > 0 && iter >= maxIter {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
