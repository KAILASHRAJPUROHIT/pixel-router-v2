// Package thermalguard ports V1's thermal_monitor.sh - the CPU/GPU
// frequency safety net that caps clocks when Android's own thermal
// status reaches SEVERE, plus a charge-current clamp when skin temp
// and battery level are both high. The one V1 script with no V2
// equivalent that's actually safety-critical (overheat/battery-wear
// prevention), so this was ported before any V1 retirement, not after.
package thermalguard

const (
	throttleStatusThreshold   = 3 // SEVERE (0=none 1=light 2=moderate 3=severe 4=critical 5=emergency 6=shutdown)
	chargeTempThresholdC      = 40.0
	chargeBatteryThresholdPct = 50
)

// Controller is everything the decision logic needs from the device -
// RealController shells out to the same dumpsys/sysfs paths V1 used;
// a fake in tests exercises Tick without a device.
type Controller interface {
	ThermalStatus() (status int, hasReading bool, err error)
	SkinTempC() (tempC float64, hasReading bool, err error)
	BatteryPercent() (pct int, hasReading bool, err error)

	ThrottleCPUs() error
	RestoreCPUs() error
	// BaselineFreqOK reports whether cpu0's current scaling_max_freq
	// already matches the intended 70% baseline - V1 found live that
	// frequency could silently drift below baseline with no thermal
	// event to explain it (a governor write race, boot-time apply
	// order, etc.), and nothing ever noticed without this check.
	BaselineFreqOK() (bool, error)

	LimitChargeCurrent() error
	RestoreChargeCurrent() error
}

// State is this service's persisted decision state across ticks -
// kept in-memory (not a flat file like V1) since one Go process now
// owns this exclusively for its whole lifetime; a restart just
// reassesses live state fresh, which is exactly as correct as V1's
// file-backed state was (both just mirror current reality).
type State struct {
	Throttled     bool
	ChargeLimited bool
}

// TickResult reports what happened, for logging - kept separate from
// the logging itself so Tick stays unit-testable without a logger.
type TickResult struct {
	ThrottleChanged    bool // now throttled or now restored
	NowThrottled       bool
	BaselineReapplied  bool
	ChargeLimitChanged bool
	NowChargeLimited   bool
}

// Tick runs one pass: thermal status drives the CPU/GPU throttle
// decision (SEVERE+ throttles, otherwise restores to baseline -
// including re-applying baseline if it's drifted with no thermal
// event to explain it); skin temp + battery level independently drive
// the charge-current clamp.
func Tick(ctrl Controller, st *State) TickResult {
	var result TickResult

	status, hasStatus, _ := ctrl.ThermalStatus()
	severe := hasStatus && status >= throttleStatusThreshold

	if severe {
		if !st.Throttled {
			ctrl.ThrottleCPUs()
			st.Throttled = true
			result.ThrottleChanged = true
		}
	} else {
		if st.Throttled {
			ctrl.RestoreCPUs()
			st.Throttled = false
			result.ThrottleChanged = true
		} else if ok, err := ctrl.BaselineFreqOK(); err == nil && !ok {
			ctrl.RestoreCPUs()
			result.BaselineReapplied = true
		}
	}
	result.NowThrottled = st.Throttled

	tempC, hasTemp, _ := ctrl.SkinTempC()
	battPct, hasBatt, _ := ctrl.BatteryPercent()
	shouldLimitCharge := hasTemp && hasBatt && tempC > chargeTempThresholdC && battPct > chargeBatteryThresholdPct

	if shouldLimitCharge && !st.ChargeLimited {
		ctrl.LimitChargeCurrent()
		st.ChargeLimited = true
		result.ChargeLimitChanged = true
	} else if !shouldLimitCharge && st.ChargeLimited {
		ctrl.RestoreChargeCurrent()
		st.ChargeLimited = false
		result.ChargeLimitChanged = true
	}
	result.NowChargeLimited = st.ChargeLimited

	return result
}
