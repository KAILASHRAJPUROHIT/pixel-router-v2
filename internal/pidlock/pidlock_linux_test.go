//go:build linux

package pidlock

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquire_SecondCallFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor.pid")

	f1, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	defer f1.Close()

	f2, err := Acquire(path)
	if err == nil {
		if f2 != nil {
			f2.Close()
		}
		t.Fatal("second acquire while first still holds the lock should fail")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("expected error to name the holding pid, got: %v", err)
	}
}

func TestAcquire_SucceedsAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor.pid")

	f1, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	f1.Close() // releases the flock

	f2, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}
	defer f2.Close()
}
