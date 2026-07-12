package dreamwatchdog

import (
	"fmt"
	"log"
	"time"
)

// TickSettingsDrift runs one pass of the slow settings/component-drift
// corrections V1's aod_watchdog.sh polled every 120s. Returns the
// names of anything it had to fix, for logging - an empty slice means
// nothing had drifted this pass.
func TickSettingsDrift(ctrl Controller) []string {
	var changed []string
	checks := []struct {
		name string
		fn   func() (bool, error)
	}{
		{"screensaver_enabled", ctrl.AssertScreensaverEnabled},
		{"screensaver_default_component", ctrl.AssertScreensaverDefaultComponent},
		{"package_verifier", ctrl.AssertPackageVerifierEnabled},
		{"router_aod_package_enabled", ctrl.AssertRouterAodPackageEnabled},
		{"adb_wifi_disabled", ctrl.AssertAdbWifiDisabled},
		{"doze_setting_disabled", ctrl.AssertDozeSettingDisabled},
		{"nfc_disabled", ctrl.AssertNfcDisabled},
		{"fixed_brightness", ctrl.AssertFixedBrightness},
		{"lowlightclock_disabled", ctrl.AssertLowLightClockDisabled},
		{"doze_service_disabled", ctrl.AssertDozeServiceDisabled},
	}
	for _, c := range checks {
		did, err := c.fn()
		if err != nil {
			log.Printf("dreamwatchdog: %s check failed: %v", c.name, err)
			continue
		}
		if did {
			changed = append(changed, c.name)
		}
	}
	return changed
}

// RecoveryState is the fast loop's adaptive-backoff state, mirroring
// aod_dream_recovery.sh's MIN/MAX interval backoff and its "sustained
// flapping" deep-check escalation.
type RecoveryState struct {
	Gate *ThermalGate

	MinInterval       time.Duration
	MaxInterval       time.Duration
	BackoffAfterOK    int
	DeepCheckAfter    int
	DeepCheckCooldown time.Duration

	healthyStreak   int
	currentInterval time.Duration
	consecMismatch  int
	lastDeepCheck   time.Time
}

func NewRecoveryState() *RecoveryState {
	return &RecoveryState{
		Gate:              NewThermalGate(),
		MinInterval:       5 * time.Second,
		MaxInterval:       60 * time.Second,
		BackoffAfterOK:    6,
		DeepCheckAfter:    5,
		DeepCheckCooldown: 60 * time.Second,
		currentInterval:   5 * time.Second,
	}
}

// TickResult reports what happened this pass, for logging - deliberately
// plain data rather than doing the logging itself, so the decision
// logic here stays unit-testable without a *log.Logger dependency.
type TickResult struct {
	NextInterval time.Duration
	ThermalPause bool
	NotCharging  bool
	Recovered    bool // ran StartDreaming this pass
	DeepCheck    bool // ran the sustained-flapping deep check this pass
	DeepCheckMsg string
	Err          error
}

// TickDreamRecovery runs one pass of the fast dream-mismatch check,
// mirroring aod_dream_recovery.sh: while thermally paused, do nothing
// (RouterDreamService's own heat cutoff is left alone); otherwise, if
// the device is dozing/asleep/dreaming and the active dream isn't
// ours, run `cmd dreams start-dreaming`. After DeepCheckAfter
// consecutive mismatches, escalate to checking for the two known real
// causes (package disabled, user still locked).
//
// Also gates on charging state (added 2026-07-12): confirmed live that
// Android's own DreamManagerService ties dream validity to charging
// (mDreamsActivatedOnChargeByDefault=true,
// mKeepDreamingWhenUnpluggingDefault=false) - while unplugged, the OS
// keeps invalidating the dream almost immediately, and this loop
// forcing it back every few seconds produced a rapid lockscreen
// flicker (~1s cadence, faster than this loop's own 5-60s interval -
// that mismatch is what pointed at an OS-level fight rather than a bug
// in this loop's own timing). This device is a stationary, always-
// plugged-in router (see aod_watchdog.sh's own "not a daily driver
// phone" comment), so the correct fix is to stop fighting for the
// dream at all while unplugged and let the screen behave normally,
// exactly like the existing thermal-pause gate already does for heat.
func TickDreamRecovery(ctrl Controller, st *RecoveryState, now time.Time) TickResult {
	tempC, hasTemp, tempErr := ctrl.SkinTempC()
	status, hasStatus, statusErr := ctrl.ThermalStatus()
	if tempErr != nil || statusErr != nil {
		// A read failure isn't treated as "hot" or "cool" - Update
		// with hasTemp/hasStatus=false leaves the gate exactly where
		// it was.
		hasTemp, hasStatus = false, false
	}
	paused := st.Gate.Update(tempC, hasTemp, status, hasStatus)
	if paused {
		st.consecMismatch = 0
		st.healthyStreak = 0
		st.currentInterval = st.MaxInterval
		return TickResult{NextInterval: st.currentInterval, ThermalPause: true}
	}

	charging, hasCharging, chargeErr := ctrl.IsCharging()
	if chargeErr == nil && hasCharging && !charging {
		st.consecMismatch = 0
		st.healthyStreak = 0
		st.currentInterval = st.MaxInterval
		return TickResult{NextInterval: st.currentInterval, NotCharging: true}
	}

	wakefulness, err := ctrl.Wakefulness()
	if err != nil {
		return TickResult{NextInterval: st.currentInterval, Err: err}
	}

	mismatch := false
	if wakefulness == "Dozing" || wakefulness == "Asleep" || wakefulness == "Dreaming" {
		isOurs, err := ctrl.CurrentDreamIsOurs()
		if err != nil {
			return TickResult{NextInterval: st.currentInterval, Err: err}
		}
		if !isOurs {
			mismatch = true
			if startErr := ctrl.StartDreaming(); startErr != nil {
				return TickResult{NextInterval: st.currentInterval, Err: startErr}
			}
			st.consecMismatch++
		}
	}

	result := TickResult{Recovered: mismatch}

	if mismatch && st.consecMismatch >= st.DeepCheckAfter && now.Sub(st.lastDeepCheck) >= st.DeepCheckCooldown {
		st.lastDeepCheck = now
		result.DeepCheck = true
		result.DeepCheckMsg = runDeepCheck(ctrl, st.consecMismatch)
	}

	if mismatch {
		st.healthyStreak = 0
		st.currentInterval = st.MinInterval
	} else {
		st.consecMismatch = 0
		st.healthyStreak++
		if st.healthyStreak >= st.BackoffAfterOK {
			st.healthyStreak = 0
			st.currentInterval *= 2
			if st.currentInterval > st.MaxInterval {
				st.currentInterval = st.MaxInterval
			}
		}
		if st.currentInterval == 0 {
			st.currentInterval = st.MinInterval
		}
	}

	result.NextInterval = st.currentInterval
	return result
}

func runDeepCheck(ctrl Controller, consecMismatch int) string {
	enabled, err := ctrl.IsRouterAodPackageEnabled()
	if err == nil && !enabled {
		if fixErr := ctrl.ForceRestartRouterAod(); fixErr == nil {
			return fmt.Sprintf("sustained flapping (%d in a row) - com.router.aod package was disabled, re-enabled", consecMismatch)
		}
	}
	locked, err := ctrl.IsUserLocked()
	if err == nil && locked {
		return fmt.Sprintf("sustained flapping (%d in a row) - user 0 is still locked (before-first-unlock); cannot be fixed automatically", consecMismatch)
	}
	return fmt.Sprintf("sustained flapping (%d in a row) - package enabled and user unlocked, cause unknown", consecMismatch)
}
