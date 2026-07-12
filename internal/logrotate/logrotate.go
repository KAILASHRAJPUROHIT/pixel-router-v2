// Package logrotate ports V1's log_rotate.sh - truncates any .log file
// over a size threshold down to its last N lines. Applied to V2's own
// log directory (/data/adb/router_v2/logs), which grew a real need for
// this the moment supervisor started capturing every service's own
// stdout/stderr into per-service files.
package logrotate

import (
	"bufio"
	"os"
	"path/filepath"
)

const (
	MaxSizeBytes = 1 * 1024 * 1024 // 1MB
	KeepLines    = 2000
)

// RotateDir truncates every *.log file in dir over MaxSizeBytes down
// to its last KeepLines lines. Returns the names of files it actually
// rotated, for logging.
func RotateDir(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		return nil, err
	}
	var rotated []string
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.Size() <= MaxSizeBytes {
			continue
		}
		if err := truncateToLastLines(path, KeepLines); err == nil {
			rotated = append(rotated, filepath.Base(path))
		}
	}
	return rotated, nil
}

// truncateToLastLines rewrites path to contain only its last n lines,
// via a temp file + rename (atomic - a reader never sees a half-
// written file, the same pattern used elsewhere in this codebase for
// exactly this reason).
func truncateToLastLines(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	lines := make([]string, 0, n)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		return err
	}

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	for _, line := range lines {
		w.WriteString(line)
		w.WriteString("\n")
	}
	if err := w.Flush(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
