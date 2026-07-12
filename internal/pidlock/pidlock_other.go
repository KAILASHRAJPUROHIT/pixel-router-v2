//go:build !linux

package pidlock

import "os"

// Acquire is a no-op stub on non-Linux platforms (e.g. running the
// Windows-native `go build ./...`/`go vet ./...` during local dev) -
// the real flock-based lock only matters on-device, where every V2
// binary actually runs.
func Acquire(path string) (*os.File, error) {
	return nil, nil
}
