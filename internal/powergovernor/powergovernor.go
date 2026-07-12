// Package powergovernor ports V1's aod_power_governor.sh - a two-tier
// CPU/GPU clock cap on top of the permanent 70%/60% baseline: drops
// further while the AOD is idly displaying (screen not actively used -
// dreaming or system Doze) since nothing latency-sensitive is
// happening, snapping back to baseline the moment the user actually
// wakes/interacts with the phone. Defers entirely to thermalguard's
// SEVERE+ emergency throttle - if that's already engaged, this leaves
// clocks alone rather than fight it (mirrors V1's own stated behavior).
package powergovernor

const (
	AODCPUPct  = 35
	AODGPUPct  = 30
	BaseCPUPct = 70
	BaseGPUPct = 60
)

type Tier string

const (
	TierBase Tier = "base"
	TierAOD  Tier = "aod"
)

type Controller interface {
	// ThermallyThrottled reports whether thermalguard's own SEVERE+
	// emergency throttle is currently engaged - this governor must not
	// fight that by trying to apply its own (higher) baseline/AOD tier.
	ThermallyThrottled() (bool, error)
	// DreamState reads RouterDreamService's own fast-path state file
	// ("dreaming"/"awake") if fresh; hasReading=false means missing or
	// stale, and the caller should fall back to Wakefulness().
	DreamState() (state string, hasReading bool, err error)
	Wakefulness() (string, error)

	SetCPUPct(pct int) error
	SetGPUPct(pct int) error
}

type State struct {
	LastTier Tier
}

type TickResult struct {
	Skipped     bool // thermally throttled - deferred entirely
	TierChanged bool
	NewTier     Tier
	Source      string // what decided the tier, for logging
}

// Tick mirrors V1's loop body: thermalguard's emergency throttle
// always wins (skip entirely); otherwise prefer the fast dream-state
// file, falling back to wakefulness, and only touch clocks when the
// resulting tier actually changed.
func Tick(ctrl Controller, st *State) TickResult {
	if throttled, err := ctrl.ThermallyThrottled(); err == nil && throttled {
		return TickResult{Skipped: true}
	}

	var wantTier Tier
	var source string

	if state, hasReading, _ := ctrl.DreamState(); hasReading {
		switch state {
		case "dreaming":
			wantTier, source = TierAOD, "dream_state=dreaming"
		case "awake":
			wantTier, source = TierBase, "dream_state=awake"
		default:
			hasReading = false
		}
		if hasReading {
			return applyIfChanged(ctrl, st, wantTier, source)
		}
	}

	wakefulness, _ := ctrl.Wakefulness()
	if wakefulness == "Awake" {
		wantTier = TierBase
	} else {
		wantTier = TierAOD
	}
	source = "wakefulness=" + wakefulness

	return applyIfChanged(ctrl, st, wantTier, source)
}

func applyIfChanged(ctrl Controller, st *State, wantTier Tier, source string) TickResult {
	if wantTier == st.LastTier {
		return TickResult{NewTier: wantTier}
	}
	if wantTier == TierAOD {
		ctrl.SetCPUPct(AODCPUPct)
		ctrl.SetGPUPct(AODGPUPct)
	} else {
		ctrl.SetCPUPct(BaseCPUPct)
		ctrl.SetGPUPct(BaseGPUPct)
	}
	st.LastTier = wantTier
	return TickResult{TierChanged: true, NewTier: wantTier, Source: source}
}
