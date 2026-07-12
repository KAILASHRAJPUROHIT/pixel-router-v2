// Package dreamwatchdog ports V1's aod_watchdog.sh (slow settings-
// drift correction) and aod_dream_recovery.sh (fast dream-takeover
// recovery) into one Go service - the one piece of V1's AOD management
// that hadn't been ported to V2 yet.
package dreamwatchdog

// ThermalGate is a hysteresis-guarded pause flag: enter pause at
// EnterC (or ge SEVERE thermal status), only leave pause once temp has
// cooled to ExitC or below. Confirmed live 2026-07-11: V1's original
// single-threshold check (pause whenever skin temp >= 45C, resume the
// instant it dropped below) fought RouterDreamService's own identical
// 45C self-shutdown threshold whenever skin temp oscillated right at
// that line - our dream would self-terminate at 45C, the watchdog
// would see "not paused" on the very next tick if temp had dipped even
// slightly, force the dream back, and it could cross 45C again within
// ~15s (RouterDreamService's own 3-poll debounce) and terminate again.
// A one-way gap means a brief dip below EnterC can no longer
// immediately flip the gate - temp has to genuinely fall to ExitC.
type ThermalGate struct {
	paused bool
	EnterC float64
	ExitC  float64
}

// NewThermalGate returns a gate starting in the not-paused state, using
// the same EnterC threshold as both V1 scripts (and RouterDreamService's
// own MAX_SAFE_TEMP_C). ExitC has only a 1C gap, not the original 3C -
// confirmed live 2026-07-12 that this device's skin temp normally
// idles in the 42-46C band under load, so a 3C gap (exit at 42C) left
// the gate stuck "paused" (and dreamwatchdog silently not fighting for
// the dream at all) for hours at a time whenever temp lingered between
// 42-45C, which is most of the time - not just during genuine thermal
// events. RouterDreamService's own 3-consecutive-poll (~15s) debounce
// before it self-terminates again already protects against a single
// momentary blip back over 45C, so a smaller gap doesn't reintroduce
// the original rapid-flapping bug this hysteresis was built to fix.
func NewThermalGate() *ThermalGate {
	return &ThermalGate{EnterC: 45.0, ExitC: 44.0}
}

// Update feeds in the latest readings and returns whether the gate is
// (now) paused. hasTemp/hasStatus being false means that reading
// wasn't available this tick (e.g. a transient dumpsys failure) - such
// a reading is simply ignored rather than treated as "not hot", so a
// temporary read failure can never itself flip the gate.
func (g *ThermalGate) Update(skinTempC float64, hasTemp bool, thermalStatus int, hasStatus bool) bool {
	severe := hasStatus && thermalStatus >= 3

	if severe {
		g.paused = true
		return true
	}

	if g.paused {
		if hasTemp && skinTempC <= g.ExitC {
			g.paused = false
		}
		return g.paused
	}

	if hasTemp && skinTempC >= g.EnterC {
		g.paused = true
	}
	return g.paused
}

// Paused reports the gate's current state without feeding in a new
// reading.
func (g *ThermalGate) Paused() bool {
	return g.paused
}
