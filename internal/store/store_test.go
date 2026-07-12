package store

import (
	"os"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "router_v2_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRecoveryCodeLifecycle(t *testing.T) {
	st := newTestStore(t)

	codes, err := st.GenerateRecoveryCodes(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 5 {
		t.Fatalf("expected 5 codes, got %d", len(codes))
	}

	remaining, _ := st.RemainingRecoveryCodes()
	if remaining != 5 {
		t.Fatalf("expected 5 remaining, got %d", remaining)
	}

	// A valid code works and is consumed (single-use).
	ok, err := st.CheckAndConsumeRecoveryCode(codes[0])
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected valid recovery code to be accepted")
	}
	remaining, _ = st.RemainingRecoveryCodes()
	if remaining != 4 {
		t.Fatalf("expected 4 remaining after use, got %d", remaining)
	}

	// The same code cannot be used twice.
	ok, err = st.CheckAndConsumeRecoveryCode(codes[0])
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected already-used recovery code to be rejected")
	}

	// A garbage code is rejected.
	ok, _ = st.CheckAndConsumeRecoveryCode("NOTREAL-CODE")
	if ok {
		t.Fatal("expected bogus code to be rejected")
	}

	// Regenerating replaces the old set entirely.
	newCodes, err := st.GenerateRecoveryCodes(3)
	if err != nil {
		t.Fatal(err)
	}
	remaining, _ = st.RemainingRecoveryCodes()
	if remaining != 3 {
		t.Fatalf("expected 3 remaining after regeneration, got %d", remaining)
	}
	ok, _ = st.CheckAndConsumeRecoveryCode(codes[1])
	if ok {
		t.Fatal("old recovery code should be invalidated after regeneration")
	}
	ok, _ = st.CheckAndConsumeRecoveryCode(newCodes[0])
	if !ok {
		t.Fatal("new recovery code should work")
	}
}

func TestLockout(t *testing.T) {
	st := newTestStore(t)
	key := "1.2.3.4:1234"

	for i := 0; i < 4; i++ {
		locked, _ := st.IsLockedOut(key)
		if locked {
			t.Fatalf("should not be locked out after %d attempts", i)
		}
		st.RecordFailedAttempt(key)
	}
	st.RecordFailedAttempt(key) // 5th failure
	locked, _ := st.IsLockedOut(key)
	if !locked {
		t.Fatal("expected lockout after 5 failed attempts")
	}

	st.ClearFailedAttempts(key)
	locked, _ = st.IsLockedOut(key)
	if locked {
		t.Fatal("expected lockout cleared after ClearFailedAttempts")
	}
}
