//go:build linux

// Package pidlock is a structural fix for the orphaned/duplicate-
// process incidents hit repeatedly on 2026-07-11: an interrupted
// restart (or simply forgetting to check for a still-running old
// instance before launching a new one) left two supervisor process
// trees alive simultaneously, one holding port 9090 and blocking the
// other's webserver/ttlproxy from ever starting. An flock-based
// single-instance lock makes a second launch fail immediately and
// loudly instead of silently racing the first for shared resources.
package pidlock

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// Acquire opens (creating if needed) the file at path and takes an
// exclusive, non-blocking flock on it. On success it writes the
// current pid into the file (for diagnostics - "who holds this lock"
// is then a plain `cat`) and returns the open *os.File, which the
// caller must keep open for the lifetime of the process; the lock is
// released automatically when the process exits or the fd is closed.
// On failure (someone else already holds it) it returns an error
// naming the pid already holding the lock, if that pid is readable.
func Acquire(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pidfile %s: %w", path, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := "unknown"
		if data, readErr := os.ReadFile(path); readErr == nil {
			if pid := string(data); pid != "" {
				holder = pid
			}
		}
		f.Close()
		return nil, fmt.Errorf("another instance already holds the lock on %s (pid %s)", path, holder)
	}

	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, fmt.Errorf("truncate pidfile: %w", err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("write pid: %w", err)
	}

	return f, nil
}
