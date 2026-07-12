package dreamwatchdog

import (
	"testing"
	"time"
)

// fakeController is a minimal, fully-configurable Controller for
// testing TickDreamRecovery's decision logic without a device. Fields
// default to "healthy" (charging, temp comfortable, dream is ours) so
// a test only needs to override what it's actually exercising.
type fakeController struct {
	skinTempC          float64
	hasSkinTemp        bool
	thermalStatus      int
	hasThermalStatus   bool
	wakefulness        string
	currentIsOurs      bool
	charging           bool
	hasCharging        bool
	chargingErr        error
	startDreamingCalls int
}

func (f *fakeController) SkinTempC() (float64, bool, error) { return f.skinTempC, f.hasSkinTemp, nil }
func (f *fakeController) ThermalStatus() (int, bool, error) {
	return f.thermalStatus, f.hasThermalStatus, nil
}
func (f *fakeController) Wakefulness() (string, error)             { return f.wakefulness, nil }
func (f *fakeController) CurrentDreamIsOurs() (bool, error)        { return f.currentIsOurs, nil }
func (f *fakeController) StartDreaming() error                     { f.startDreamingCalls++; return nil }
func (f *fakeController) IsRouterAodPackageEnabled() (bool, error) { return true, nil }
func (f *fakeController) IsUserLocked() (bool, error)              { return false, nil }
func (f *fakeController) IsCharging() (bool, bool, error) {
	return f.charging, f.hasCharging, f.chargingErr
}

func (f *fakeController) AssertScreensaverEnabled() (bool, error)          { return false, nil }
func (f *fakeController) AssertScreensaverDefaultComponent() (bool, error) { return false, nil }
func (f *fakeController) AssertPackageVerifierEnabled() (bool, error)      { return false, nil }
func (f *fakeController) AssertRouterAodPackageEnabled() (bool, error)     { return false, nil }
func (f *fakeController) AssertAdbWifiDisabled() (bool, error)             { return false, nil }
func (f *fakeController) AssertDozeSettingDisabled() (bool, error)         { return false, nil }
func (f *fakeController) AssertNfcDisabled() (bool, error)                 { return false, nil }
func (f *fakeController) AssertFixedBrightness() (bool, error)             { return false, nil }
func (f *fakeController) AssertLowLightClockDisabled() (bool, error)       { return false, nil }
func (f *fakeController) AssertDozeServiceDisabled() (bool, error)         { return false, nil }
func (f *fakeController) ForceRestartRouterAod() error                     { return nil }

func newHealthyFakeController() *fakeController {
	return &fakeController{
		skinTempC:        35.0,
		hasSkinTemp:      true,
		thermalStatus:    0,
		hasThermalStatus: true,
		wakefulness:      "Asleep",
		currentIsOurs:    false, // a mismatch to recover from, by default
		charging:         true,
		hasCharging:      true,
	}
}

func TestTickDreamRecovery_NotChargingSkipsRecovery(t *testing.T) {
	ctrl := newHealthyFakeController()
	ctrl.charging = false

	st := NewRecoveryState()
	result := TickDreamRecovery(ctrl, st, time.Now())

	if !result.NotCharging {
		t.Fatal("expected NotCharging=true when unplugged")
	}
	if ctrl.startDreamingCalls != 0 {
		t.Fatalf("expected no start-dreaming attempts while unplugged, got %d", ctrl.startDreamingCalls)
	}
	if result.NextInterval != st.MaxInterval {
		t.Fatalf("expected backoff to MaxInterval while unplugged, got %s", result.NextInterval)
	}
}

func TestTickDreamRecovery_ChargingRunsNormalRecovery(t *testing.T) {
	ctrl := newHealthyFakeController()
	st := NewRecoveryState()

	result := TickDreamRecovery(ctrl, st, time.Now())

	if result.NotCharging {
		t.Fatal("expected NotCharging=false while charging")
	}
	if !result.Recovered {
		t.Fatal("expected recovery to run and fire (dream wasn't ours)")
	}
	if ctrl.startDreamingCalls != 1 {
		t.Fatalf("expected exactly one start-dreaming call, got %d", ctrl.startDreamingCalls)
	}
}

func TestTickDreamRecovery_ChargingReadErrorFailsOpen(t *testing.T) {
	ctrl := newHealthyFakeController()
	ctrl.hasCharging = false // simulate a read failure - no reading available

	st := NewRecoveryState()
	result := TickDreamRecovery(ctrl, st, time.Now())

	if result.NotCharging {
		t.Fatal("expected NotCharging=false (fail open) when charging state is unknown")
	}
	if !result.Recovered {
		t.Fatal("expected normal recovery to proceed when charging state can't be read")
	}
}

func TestTickDreamRecovery_ThermalPauseTakesPrecedenceOverCharging(t *testing.T) {
	ctrl := newHealthyFakeController()
	ctrl.charging = false
	ctrl.skinTempC = 46.0 // also hot

	st := NewRecoveryState()
	result := TickDreamRecovery(ctrl, st, time.Now())

	if !result.ThermalPause {
		t.Fatal("expected ThermalPause to be reported when both hot and unplugged")
	}
	if result.NotCharging {
		t.Fatal("expected NotCharging not to be separately reported once ThermalPause already applies")
	}
}
