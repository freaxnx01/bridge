package store

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

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
