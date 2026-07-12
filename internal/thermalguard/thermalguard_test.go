package thermalguard

import "testing"

type fakeController struct {
	status     int
	hasStatus  bool
	tempC      float64
	hasTemp    bool
	battPct    int
	hasBatt    bool
	baselineOK bool

	throttleCalls      int
	restoreCalls       int
	baselineCalls      int
	limitCalls         int
	restoreChargeCalls int
}

func (f *fakeController) ThermalStatus() (int, bool, error)  { return f.status, f.hasStatus, nil }
func (f *fakeController) SkinTempC() (float64, bool, error)  { return f.tempC, f.hasTemp, nil }
func (f *fakeController) BatteryPercent() (int, bool, error) { return f.battPct, f.hasBatt, nil }
func (f *fakeController) ThrottleCPUs() error                { f.throttleCalls++; return nil }
func (f *fakeController) RestoreCPUs() error                 { f.restoreCalls++; return nil }
func (f *fakeController) BaselineFreqOK() (bool, error) {
	f.baselineCalls++
	return f.baselineOK, nil
}
func (f *fakeController) LimitChargeCurrent() error   { f.limitCalls++; return nil }
func (f *fakeController) RestoreChargeCurrent() error { f.restoreChargeCalls++; return nil }

func healthy() *fakeController {
	return &fakeController{status: 0, hasStatus: true, tempC: 30, hasTemp: true, battPct: 50, hasBatt: true, baselineOK: true}
}

func TestTick_SevereThrottles(t *testing.T) {
	ctrl := healthy()
	ctrl.status = 3
	st := &State{}
	res := Tick(ctrl, st)

	if !res.ThrottleChanged || !res.NowThrottled {
		t.Fatal("expected throttle to engage at SEVERE")
	}
	if ctrl.throttleCalls != 1 {
		t.Fatalf("expected ThrottleCPUs called once, got %d", ctrl.throttleCalls)
	}
}

func TestTick_RestoresWhenNoLongerSevere(t *testing.T) {
	ctrl := healthy()
	st := &State{Throttled: true}
	res := Tick(ctrl, st)

	if !res.ThrottleChanged || res.NowThrottled {
		t.Fatal("expected restore when status back to normal")
	}
	if ctrl.restoreCalls != 1 {
		t.Fatalf("expected RestoreCPUs called once, got %d", ctrl.restoreCalls)
	}
}

func TestTick_ReappliesDriftedBaseline(t *testing.T) {
	ctrl := healthy()
	ctrl.baselineOK = false
	st := &State{}
	res := Tick(ctrl, st)

	if !res.BaselineReapplied {
		t.Fatal("expected baseline reapply when drifted with no thermal event")
	}
	if ctrl.restoreCalls != 1 {
		t.Fatalf("expected RestoreCPUs called to reapply baseline, got %d calls", ctrl.restoreCalls)
	}
}

func TestTick_NoChangeWhenAlreadyThrottledAndStillSevere(t *testing.T) {
	ctrl := healthy()
	ctrl.status = 4
	st := &State{Throttled: true}
	res := Tick(ctrl, st)

	if res.ThrottleChanged {
		t.Fatal("expected no change - already throttled and still severe")
	}
	if ctrl.throttleCalls != 0 {
		t.Fatalf("expected no redundant ThrottleCPUs call, got %d", ctrl.throttleCalls)
	}
}

func TestTick_ChargeLimitEngagesWhenHotAndCharged(t *testing.T) {
	ctrl := healthy()
	ctrl.tempC = 41.0
	ctrl.battPct = 60
	st := &State{}
	res := Tick(ctrl, st)

	if !res.ChargeLimitChanged || !res.NowChargeLimited {
		t.Fatal("expected charge limit to engage above 40C and 50% battery")
	}
	if ctrl.limitCalls != 1 {
		t.Fatalf("expected LimitChargeCurrent called once, got %d", ctrl.limitCalls)
	}
}

func TestTick_ChargeLimitDoesNotEngageBelowThresholds(t *testing.T) {
	ctrl := healthy()
	ctrl.tempC = 39.0
	ctrl.battPct = 60
	st := &State{}
	res := Tick(ctrl, st)

	if res.ChargeLimitChanged || res.NowChargeLimited {
		t.Fatal("expected no charge limit below the temp threshold")
	}
}

func TestTick_ChargeLimitReleasesOnceCooled(t *testing.T) {
	ctrl := healthy()
	st := &State{ChargeLimited: true}
	res := Tick(ctrl, st)

	if !res.ChargeLimitChanged || res.NowChargeLimited {
		t.Fatal("expected charge limit to release once cooled")
	}
	if ctrl.restoreChargeCalls != 1 {
		t.Fatalf("expected RestoreChargeCurrent called once, got %d", ctrl.restoreChargeCalls)
	}
}

func TestTick_MissingReadingsDoNotChangeState(t *testing.T) {
	ctrl := &fakeController{hasStatus: false, hasTemp: false, hasBatt: false, baselineOK: true}
	st := &State{}
	res := Tick(ctrl, st)

	if res.ThrottleChanged || res.NowThrottled || res.ChargeLimitChanged || res.NowChargeLimited {
		t.Fatal("expected no state changes when no readings are available")
	}
}
