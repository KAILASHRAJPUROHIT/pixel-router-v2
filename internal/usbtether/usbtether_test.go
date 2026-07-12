package usbtether

import (
	"errors"
	"testing"
	"time"
)

// fakeStarter lets tests drive EnsureUp's retry logic without ever
// invoking app_process or touching a real ncm0 interface. isUpAfter
// simulates IsUp() becoming true after a given number of Start() calls
// by swapping in a package-level override during the test.
type fakeStarter struct {
	calls     int
	startErr  error
	succeedAt int // Start() call number at which isUpFn should report true
}

func (f *fakeStarter) Start() (string, error) {
	f.calls++
	return "fake output", f.startErr
}

func withFakeIsUp(t *testing.T, succeedAtCall int, calls *int) {
	t.Helper()
	orig := isUpFn
	isUpFn = func() bool {
		return *calls >= succeedAtCall
	}
	t.Cleanup(func() { isUpFn = orig })
}

func TestEnsureUp_SucceedsOnFirstAttempt(t *testing.T) {
	f := &fakeStarter{}
	withFakeIsUp(t, 1, &f.calls)

	attempts, _, err := EnsureUp(f, 4, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestEnsureUp_SucceedsOnThirdAttempt(t *testing.T) {
	f := &fakeStarter{}
	withFakeIsUp(t, 3, &f.calls)

	attempts, _, err := EnsureUp(f, 4, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if f.calls != 3 {
		t.Fatalf("expected Start() called 3 times, got %d", f.calls)
	}
}

func TestEnsureUp_ExhaustsAttempts_ReturnsError(t *testing.T) {
	f := &fakeStarter{}
	withFakeIsUp(t, 100, &f.calls) // never succeeds within maxAttempts

	attempts, _, err := EnsureUp(f, 4, time.Millisecond)
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if attempts != 4 {
		t.Fatalf("expected 4 attempts made, got %d", attempts)
	}
	if f.calls != 4 {
		t.Fatalf("expected Start() called 4 times, got %d", f.calls)
	}
}

func TestEnsureUp_StartErrorDoesNotStopRetrying(t *testing.T) {
	f := &fakeStarter{startErr: errors.New("app_process failed")}
	withFakeIsUp(t, 2, &f.calls)

	attempts, output, err := EnsureUp(f, 4, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if output == "" {
		t.Fatal("expected non-empty output even when Start() errored")
	}
}
