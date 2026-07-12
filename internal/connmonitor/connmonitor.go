// Package connmonitor consolidates three V1 scripts that all guarded
// cellular/detection health from different angles:
//   - connection_optimizer.sh (4h): settings drift (conntrack, private
//     DNS, softap timeout) + signal-based reconnect
//   - connection_stability_poller.sh (15min): the same signal-based
//     reconnect at a much faster cadence (V1's own comment: 4h was
//     "far too slow to catch a bad stretch"), plus ping loss/latency
//     and data-registration checks
//   - airtel_detection_safety_poller.sh (1min): confirms the TTL-proxy
//     tethering-detection protection is genuinely wired up (not just
//     "is the process alive"), since iptables rules can be flushed by
//     something else while the process itself stays running
//
// The 15min stability check supersedes the 4h one's reconnect logic
// (same decision, checked 16x more often), so settings-drift and
// stability/reconnect are folded into one periodic tick here; the
// detection-safety check keeps its own faster, independent cadence.
package connmonitor

import "time"

const (
	pingLossThresholdPct   = 40
	pingLatencyThresholdMs = 1500.0
	rsrpThreshold          = -108
	sinrThreshold          = 5
	expectedNetworkMode    = "27"
)

type Controller interface {
	// Detection safety (fast cadence).
	TTLProxyRulesIntact() (bool, error)
	RestartTTLProxy() error
	HoppatchDisableFlagPresent() (bool, error)
	CreateHoppatchDisableFlag() error

	// Stability + reconnect.
	PingStats(target string, count int) (lossPct int, avgRTTms float64, hasReading bool, err error)
	SignalReading() (rsrp int, sinr int, hasReading bool, err error)
	DataRegistrationState() (state int, hasReading bool, err error)
	IsManualNetworkSelection() (bool, error)
	ReconnectCellular() error

	// Settings drift, checked alongside the stability tick.
	AssertConntrackMax() (changed bool, err error)
	AssertPrivateDNS() (changed bool, err error)
	AssertSoftApTimeoutDisabled() (changed bool, err error)
	AssertPreferredNetworkMode() (changed bool, err error)
}

// DetectionSafetyResult reports one pass of the fast ttlproxy/hoppatch
// check, for logging.
type DetectionSafetyResult struct {
	WasIntact           bool
	RestartAttempted    bool
	RestartConfirmed    bool
	HoppatchFlagCreated bool
	Err                 error
}

// TickDetectionSafety mirrors airtel_detection_safety_poller.sh: if the
// TTL-proxy protection isn't genuinely wired up (process dead, or its
// iptables rules missing even though the process is alive), force a
// clean restart and re-verify; independently, keep the hoppatch
// disable flag in place (hoppatch breaks root on this device).
func TickDetectionSafety(ctrl Controller, recheckDelay func()) DetectionSafetyResult {
	var result DetectionSafetyResult

	intact, err := ctrl.TTLProxyRulesIntact()
	result.WasIntact = intact
	if err != nil {
		result.Err = err
	} else if !intact {
		result.RestartAttempted = true
		if restartErr := ctrl.RestartTTLProxy(); restartErr != nil {
			result.Err = restartErr
		} else {
			if recheckDelay != nil {
				recheckDelay()
			}
			confirmed, _ := ctrl.TTLProxyRulesIntact()
			result.RestartConfirmed = confirmed
		}
	}

	present, err := ctrl.HoppatchDisableFlagPresent()
	if err == nil && !present {
		if createErr := ctrl.CreateHoppatchDisableFlag(); createErr == nil {
			result.HoppatchFlagCreated = true
		}
	}

	return result
}

// StabilityResult reports one pass of the stability/settings-drift
// tick, for logging.
type StabilityResult struct {
	DriftFixed         []string
	Unstable           bool
	Reason             string
	ReconnectAttempted bool
	ManualSelection    bool
	Err                error
}

// TickStability mirrors connection_stability_poller.sh's decision
// tree (ping loss/latency, data registration, signal quality - any one
// bad triggers a guarded cellular reconnect) plus
// connection_optimizer.sh's settings-drift reassertions, run on the
// same cadence since the drift checks are cheap and idempotent.
func TickStability(ctrl Controller) StabilityResult {
	var result StabilityResult

	type driftCheck struct {
		name string
		fn   func() (bool, error)
	}
	for _, c := range []driftCheck{
		{"conntrack_max", ctrl.AssertConntrackMax},
		{"private_dns", ctrl.AssertPrivateDNS},
		{"soft_ap_timeout", ctrl.AssertSoftApTimeoutDisabled},
		{"preferred_network_mode", ctrl.AssertPreferredNetworkMode},
	} {
		if changed, err := c.fn(); err == nil && changed {
			result.DriftFixed = append(result.DriftFixed, c.name)
		}
	}

	if manual, err := ctrl.IsManualNetworkSelection(); err == nil {
		result.ManualSelection = manual
	}

	lossPct, avgRTT, hasPing, pingErr := ctrl.PingStats("8.8.8.8", 5)
	rsrp, sinr, hasSignal, sigErr := ctrl.SignalReading()
	dataReg, hasDataReg, _ := ctrl.DataRegistrationState()

	switch {
	case hasPing && lossPct >= pingLossThresholdPct:
		result.Unstable = true
		result.Reason = "packet loss"
	case hasPing && avgRTT > pingLatencyThresholdMs:
		result.Unstable = true
		result.Reason = "high latency"
	case hasDataReg && dataReg != 0:
		result.Unstable = true
		result.Reason = "data registration not IN_SERVICE"
	case hasSignal && (rsrp < rsrpThreshold || sinr < sinrThreshold):
		result.Unstable = true
		result.Reason = "poor signal"
	}

	if pingErr != nil || sigErr != nil {
		result.Err = pingErr
		if result.Err == nil {
			result.Err = sigErr
		}
	}

	if result.Unstable {
		result.ReconnectAttempted = true
		if err := ctrl.ReconnectCellular(); err != nil {
			result.Err = err
		}
	}

	return result
}

// StabilityInterval and DetectionSafetyInterval are the default
// cadences, matching V1's own (connection_stability_poller.sh's 15min,
// airtel_detection_safety_poller.sh's 1min).
const (
	StabilityInterval       = 15 * time.Minute
	DetectionSafetyInterval = 1 * time.Minute
)
