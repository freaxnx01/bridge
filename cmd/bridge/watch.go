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

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/store"
)

var (
	watchStatus    bool
	watchStop      bool
	watchDaemonize bool
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Long-running watcher of ~/projects/repos/",
	RunE:  runWatch,
}

func init() {
	watchCmd.Flags().BoolVar(&watchStatus, "status", false, "report whether a watcher is running")
	watchCmd.Flags().BoolVar(&watchStop, "stop", false, "signal a running watcher to exit")
	watchCmd.Flags().BoolVar(&watchDaemonize, "daemonize", false, "re-exec self detached")
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	pidPath := filepath.Join(cacheRoot(), "watch.pid")

	if watchStatus {
		pid, _ := store.ReadPIDFile(pidPath)
		if pid > 0 && store.IsPIDRunning(pid) {
			fmt.Fprintf(cmd.OutOrStdout(), "watch: running (PID %d)\n", pid)
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "watch: not running")
		return nil
	}
	if watchStop {
		pid, _ := store.ReadPIDFile(pidPath)
		if pid <= 0 || !store.IsPIDRunning(pid) {
			fmt.Fprintln(cmd.ErrOrStderr(), "watch: nothing to stop")
			return nil
		}
		proc, _ := os.FindProcess(pid)
		_ = proc.Signal(syscall.SIGTERM)
		return nil
	}
	if watchDaemonize {
		argv := append([]string{}, os.Args[1:]...)
		for i, a := range argv {
			if a == "--daemonize" {
				argv = append(argv[:i], argv[i+1:]...)
				break
			}
		}
		c := osExecSelf(argv)
		if err := c.Start(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "watch: detached (PID %d)\n", c.Process.Pid)
		return nil
	}

	if existing, _ := store.ReadPIDFile(pidPath); existing > 0 && store.IsPIDRunning(existing) {
		return fmt.Errorf("watch already running (PID %d)", existing)
	}
	if err := store.WritePIDFile(pidPath, os.Getpid()); err != nil {
		return err
	}
	defer store.RemovePIDFile(pidPath)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Add(reposRoot()); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	maxIter := 0
	if v := os.Getenv("BRIDGE_DAEMON_MAX_ITERATIONS"); v != "" {
		n, _ := strconv.Atoi(v)
		maxIter = n
	}

	tickInterval := 30 * time.Second
	if v := os.Getenv("BRIDGE_WATCH_TICK_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tickInterval = time.Duration(n) * time.Millisecond
		}
	}

	iter := 0
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case ev := <-w.Events:
			slog.Info("watch event", "op", ev.Op.String(), "name", ev.Name)
			_ = store.AtomicWrite(filepath.Join(cacheRoot(), "watch.last"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
		case err := <-w.Errors:
			slog.Warn("watch error", "err", err)
		case <-tick.C:
			_ = store.AtomicWrite(filepath.Join(cacheRoot(), "watch.last"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
		case <-ctx.Done():
			return nil
		}
		iter++
		if maxIter > 0 && iter >= maxIter {
			return nil
		}
	}
}
