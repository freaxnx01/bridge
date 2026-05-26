package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrAlreadyRunning is returned by AcquirePIDFile when another live process
// holds the pidfile.
var ErrAlreadyRunning = errors.New("already running")

// WritePIDFile atomically writes pid to path.
func WritePIDFile(path string, pid int) error {
	return AtomicWrite(path, []byte(strconv.Itoa(pid)+"\n"))
}

// ReadPIDFile returns the PID stored at path, or 0 if missing.
func ReadPIDFile(path string) (int, error) {
	b, err := ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

// RemovePIDFile deletes path; no error if missing.
func RemovePIDFile(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// AcquirePIDFile atomically claims path for the current process. Uses
// O_CREATE|O_EXCL to make the create-or-fail check race-free at the FS level.
// If the file already exists, the holder PID is checked: live → ErrAlreadyRunning,
// dead → file is removed and we retry once.
// Returns a release func that removes the file; safe to call on a defer.
func AcquirePIDFile(path string) (release func() error, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	pid := os.Getpid()
	for retry := 0; retry < 2; retry++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if _, werr := f.WriteString(strconv.Itoa(pid) + "\n"); werr != nil {
				f.Close()
				os.Remove(path)
				return nil, werr
			}
			if cerr := f.Close(); cerr != nil {
				os.Remove(path)
				return nil, cerr
			}
			return func() error { return RemovePIDFile(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// File exists; check whether the holder is alive.
		existing, _ := ReadPIDFile(path)
		if existing > 0 && IsPIDRunning(existing) {
			return nil, ErrAlreadyRunning
		}
		// Stale pidfile (writer crashed). Remove and retry.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return nil, rerr
		}
	}
	return nil, errors.New("pidfile: race acquiring after retry")
}

// IsPIDRunning reports whether a process with pid currently exists.
// Cross-platform: signal 0 probes existence on Unix; on Windows os.FindProcess
// returns a handle even for non-running PIDs, but the Signal(0) call will fail.
func IsPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}
