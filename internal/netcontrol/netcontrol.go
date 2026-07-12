// Package netcontrol abstracts the risky, device-only system actions
// (reading live cellular signal state, toggling airplane mode to force
// a reconnect) behind an interface - directly addressing a real gap
// found in V1: every single fix there had to be validated by hand
// against the live production phone, because the decision logic
// (when is the connection "unstable enough" to warrant a disruptive
// reconnect) was inline in the same shell script that actually
// executed `settings put global airplane_mode_on`. Splitting the two
// means the decision logic below can run in a real unit test suite
// with a fake Controller, with zero root/device required - the real
// Controller implementation is exercised only on-device, separately.
package netcontrol

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dumpsysTimeout bounds GetSignalInfo's dumpsys shell-out so a slow
// system server (e.g. under thermal throttling) degrades this one
// reading to an error rather than blocking whatever called it -
// notably the aod-stats HTTP handler, which otherwise has no bound of
// its own on this call.
const dumpsysTimeout = 3 * time.Second

type SignalInfo struct {
	RSRP             int // dBm, higher (less negative) is better
	SINR             int // dB, higher is better
	DataRegInService bool
}

// Controller is everything the decision logic in EvaluateAndHeal needs
// from the outside world. RealController talks to the actual device;
// FakeController (in netcontrol_fake.go) is for tests.
type Controller interface {
	GetSignalInfo() (SignalInfo, error)
	ReconnectCellular() error
}

// RealController shells out to the same dumpsys/settings commands V1's
// shell scripts used, now behind one interface instead of duplicated
// across multiple scripts (connection_optimizer.sh and
// connection_stability_poller.sh each had their own copy of this exact
// RSRP/SINR extraction regex in V1).
type RealController struct{}

var lteSignalRe = regexp.MustCompile(`mLte=CellSignalStrengthLte:[^,]*rsrp=(-?\d+)`)
var rssnrRe = regexp.MustCompile(`rssnr=(-?\d+)`)
var dataRegRe = regexp.MustCompile(`mDataRegState=(\d+)`)

func (RealController) GetSignalInfo() (SignalInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dumpsysTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dumpsys", "telephony.registry").CombinedOutput()
	if err != nil {
		return SignalInfo{}, fmt.Errorf("dumpsys telephony.registry: %w", err)
	}
	text := string(out)

	info := SignalInfo{}
	if m := lteSignalRe.FindStringSubmatch(text); m != nil {
		info.RSRP, _ = strconv.Atoi(m[1])
	} else {
		return info, fmt.Errorf("no LTE signal reading found (sentinel/unknown)")
	}
	if m := rssnrRe.FindStringSubmatch(text); m != nil {
		info.SINR, _ = strconv.Atoi(m[1])
	}
	if m := dataRegRe.FindStringSubmatch(text); m != nil {
		info.DataRegInService = m[1] == "0"
	}
	return info, nil
}

// ReconnectCellular toggles airplane mode off/on to force a fresh
// cellular attach. Guarantees airplane mode ends up back off even if
// interrupted mid-toggle - the same guard V1 added after a real
// outage where an earlier, unguarded version of this exact pattern
// left airplane mode stuck on.
func (RealController) ReconnectCellular() error {
	restoreOff := func() { exec.Command("settings", "put", "global", "airplane_mode_on", "0").Run() }
	defer restoreOff()

	if err := exec.Command("settings", "put", "global", "airplane_mode_on", "1").Run(); err != nil {
		return fmt.Errorf("enable airplane mode: %w", err)
	}
	time.Sleep(3 * time.Second)
	if err := exec.Command("settings", "put", "global", "airplane_mode_on", "0").Run(); err != nil {
		return fmt.Errorf("disable airplane mode: %w", err)
	}
	return nil
}

// HealthThresholds mirrors the values V1's connection_optimizer.sh and
// connection_stability_poller.sh converged on independently - kept as
// one named struct here instead of two separately-duplicated constants.
type HealthThresholds struct {
	MinRSRP int
	MinSINR int
}

var DefaultThresholds = HealthThresholds{MinRSRP: -108, MinSINR: 5}

// EvaluateAndHeal is the actual decision logic - unit-testable in full
// against a FakeController, with no device involved. Returns whether a
// reconnect was triggered and why, so callers can log/report it.
func EvaluateAndHeal(ctrl Controller, t HealthThresholds) (healed bool, reason string, err error) {
	info, err := ctrl.GetSignalInfo()
	if err != nil {
		return false, "", fmt.Errorf("get signal info: %w", err)
	}

	var reasons []string
	if !info.DataRegInService {
		reasons = append(reasons, "data registration not in service")
	}
	if info.RSRP < t.MinRSRP {
		reasons = append(reasons, fmt.Sprintf("RSRP %d below threshold %d", info.RSRP, t.MinRSRP))
	}
	if info.SINR < t.MinSINR {
		reasons = append(reasons, fmt.Sprintf("SINR %d below threshold %d", info.SINR, t.MinSINR))
	}

	if len(reasons) == 0 {
		return false, "", nil
	}

	reason = strings.Join(reasons, "; ")
	if err := ctrl.ReconnectCellular(); err != nil {
		return false, reason, fmt.Errorf("reconnect failed: %w", err)
	}
	return true, reason, nil
}
