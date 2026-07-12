package quota

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/antriva/pixel-router-v2/internal/store"
)

type fakeController struct {
	bytesSeries   []int64
	call          int
	disableCalls  int
	enableCalls   int
	notifyCalls   int
	lastNotifyMsg string
	disableErr    error
	enableErr     error
	notifyErr     error
}

func (f *fakeController) CurrentRmnetBytes() (int64, error) {
	v := f.bytesSeries[f.call]
	if f.call < len(f.bytesSeries)-1 {
		f.call++
	}
	return v, nil
}
func (f *fakeController) DisableMobileData() error { f.disableCalls++; return f.disableErr }
func (f *fakeController) EnableMobileData() error  { f.enableCalls++; return f.enableErr }
func (f *fakeController) NotifyCutoff(msg string) error {
	f.notifyCalls++
	f.lastNotifyMsg = msg
	return f.notifyErr
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	fh, err := os.CreateTemp("", "quota_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	fh.Close()
	path := fh.Name()
	t.Cleanup(func() { os.Remove(path) })
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestTick_AccumulatesBytes_BelowCutoff_NoAction(t *testing.T) {
	st := newTestStore(t)
	f := &fakeController{bytesSeries: []int64{1_000_000_000}}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	if err := Tick(f, st, Config{CutoffGB: 300}, now); err != nil {
		t.Fatal(err)
	}
	bc, _ := st.GetBillingCycle()
	if bc.CycleBytes != 1_000_000_000 {
		t.Fatalf("expected 1e9 bytes accumulated, got %d", bc.CycleBytes)
	}
	if f.disableCalls != 0 {
		t.Fatal("should not disable data far below cutoff")
	}
}

func TestTick_CrossesCutoff_DisablesAndNotifies(t *testing.T) {
	st := newTestStore(t)
	// 300GB in bytes.
	f := &fakeController{bytesSeries: []int64{300_000_000_000}}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	if err := Tick(f, st, Config{CutoffGB: 300}, now); err != nil {
		t.Fatal(err)
	}
	if f.disableCalls != 1 {
		t.Fatalf("expected DisableMobileData called once, got %d", f.disableCalls)
	}
	if f.notifyCalls != 1 {
		t.Fatalf("expected NotifyCutoff called once, got %d", f.notifyCalls)
	}
	if f.lastNotifyMsg == "" {
		t.Fatal("expected a non-empty notification message")
	}
	bc, _ := st.GetBillingCycle()
	if !bc.CutoffActive {
		t.Fatal("expected cutoff_active to be persisted as true")
	}
}

func TestTick_CutoffAlreadyActive_DoesNotDisableTwice(t *testing.T) {
	st := newTestStore(t)
	f := &fakeController{bytesSeries: []int64{300_000_000_000, 310_000_000_000}}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	Tick(f, st, Config{CutoffGB: 300}, now)
	if err := Tick(f, st, Config{CutoffGB: 300}, now); err != nil {
		t.Fatal(err)
	}
	if f.disableCalls != 1 {
		t.Fatalf("expected DisableMobileData called only once total, got %d", f.disableCalls)
	}
	if f.notifyCalls != 1 {
		t.Fatalf("expected exactly 1 notification even after a second over-cutoff tick, got %d", f.notifyCalls)
	}
}

func TestTick_CounterReset_TreatsWholeReadingAsDelta(t *testing.T) {
	st := newTestStore(t)
	// First tick establishes prevSum=5e9. Second tick's counter dropped
	// to 1e9 (interface reset) - should add 1e9, not go negative.
	f := &fakeController{bytesSeries: []int64{5_000_000_000, 1_000_000_000}}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	Tick(f, st, Config{CutoffGB: 300}, now)
	Tick(f, st, Config{CutoffGB: 300}, now)

	bc, _ := st.GetBillingCycle()
	if bc.CycleBytes != 6_000_000_000 {
		t.Fatalf("expected 5e9+1e9=6e9 accumulated across a counter reset, got %d", bc.CycleBytes)
	}
}

func TestTick_BillingDayRollover_ResetsCounterAndReEnablesData(t *testing.T) {
	st := newTestStore(t)
	st.SetBillingDay(15)
	// Simulate an already-cut-off state from the previous cycle.
	bc, _ := st.GetBillingCycle()
	bc.CutoffActive = true
	bc.CycleBytes = 300_000_000_000
	bc.LastResetMonth = "2026-06"
	st.SaveBillingCycle(bc)

	f := &fakeController{bytesSeries: []int64{0}}
	rolloverDay := time.Date(2026, 7, 15, 0, 5, 0, 0, time.UTC)

	if err := Tick(f, st, Config{CutoffGB: 300}, rolloverDay); err != nil {
		t.Fatal(err)
	}
	if f.enableCalls != 1 {
		t.Fatalf("expected EnableMobileData called once on rollover, got %d", f.enableCalls)
	}
	bc, _ = st.GetBillingCycle()
	if bc.CutoffActive {
		t.Fatal("expected cutoff_active cleared after rollover")
	}
	if bc.CycleBytes != 0 {
		t.Fatalf("expected cycle bytes reset to 0, got %d", bc.CycleBytes)
	}
}

func TestTick_RolloverOnlyFiresOncePerMonth(t *testing.T) {
	st := newTestStore(t)
	st.SetBillingDay(15)
	f := &fakeController{bytesSeries: []int64{1_000_000_000, 2_000_000_000, 3_000_000_000}}
	rolloverDay := time.Date(2026, 7, 15, 0, 5, 0, 0, time.UTC)

	Tick(f, st, Config{CutoffGB: 300}, rolloverDay)
	bc, _ := st.GetBillingCycle()
	afterFirst := bc.CycleBytes

	// Same day, later poll - must NOT reset again (V1's own guard
	// against re-resetting on every 5-minute poll throughout the day).
	Tick(f, st, Config{CutoffGB: 300}, rolloverDay.Add(2*time.Hour))
	bc, _ = st.GetBillingCycle()
	if bc.CycleBytes < afterFirst {
		t.Fatal("cycle bytes should keep accumulating, not reset again on the same billing day")
	}
}

func TestTick_DisableError_Propagates(t *testing.T) {
	st := newTestStore(t)
	f := &fakeController{bytesSeries: []int64{300_000_000_000}, disableErr: errors.New("svc data disable failed")}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	if err := Tick(f, st, Config{CutoffGB: 300}, now); err == nil {
		t.Fatal("expected error to propagate when disabling data fails")
	}
}
