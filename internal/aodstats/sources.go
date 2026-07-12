package aodstats

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dumpsysTimeout bounds every dumpsys shell-out used by this package.
// Confirmed live 2026-07-11 that under thermal throttling an
// unbounded exec.Command here can turn a normally-instant dumpsys
// call into a multi-second (or, under load, effectively unbounded)
// block inside the HTTP handler serving /internal/aod-stats - which
// reads to the AOD app's poller as "AOD struggling" (stale/missing
// stats). A bounded timeout means a slow reading degrades to "n/a"
// (already handled gracefully by aodstats.Collect) instead of
// stalling the whole response.
const dumpsysTimeout = 3 * time.Second

// RealBatteryPercent reads the kernel power-supply node directly -
// same source V1's thermal_monitor.sh used, no dumpsys parsing needed.
func RealBatteryPercent() (int, error) {
	data, err := os.ReadFile("/sys/class/power_supply/battery/capacity")
	if err != nil {
		return 0, fmt.Errorf("read battery capacity: %w", err)
	}
	pct, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse battery capacity: %w", err)
	}
	return pct, nil
}

var mValueRe = regexp.MustCompile(`mValue=([0-9.]+)`)

// RealSkinTempC parses dumpsys thermalservice's VIRTUAL-SKIN sensor -
// the same authoritative source V1's thermal_monitor.sh used, more
// meaningful than a raw thermal-zone file since it's Android's own
// user-facing skin-temperature estimate (what drives the OS's own
// thermal throttling decisions).
//
// Confirmed live 2026-07-12 that an earlier version of this used one
// regex spanning the whole (multi-line) dumpsys output looking for
// "VIRTUAL-SKIN" and then the NEXT "mValue=" after it - but each real
// entry actually reads "Temperature{mValue=X, mType=N, mName=VIRTUAL-
// SKIN, mStatus=S}", i.e. mValue appears BEFORE mName on the same
// line, so that regex always skipped past the real reading and
// grabbed whatever unrelated sensor's Temperature{} block happened to
// come next in the dump (caught red-handed grabbing usb_pwr_therm's
// 45.7C instead of the real 41.0C skin reading). Scanning line by line
// and requiring both "VIRTUAL-SKIN" and "mValue=" on the SAME line -
// matching V1's own working `grep "Temperature{.*VIRTUAL-SKIN"`
// approach - fixes this. Takes the last matching line (V1 used
// `tail -1`), since dumpsys lists more than one VIRTUAL-SKIN entry.
func RealSkinTempC() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dumpsysTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dumpsys", "thermalservice").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("dumpsys thermalservice: %w", err)
	}
	return parseVirtualSkinTemp(string(out))
}

// parseVirtualSkinTemp is split out from RealSkinTempC purely so the
// parsing logic itself (the actual site of the 2026-07-12 bug) is
// unit-testable without a device.
func parseVirtualSkinTemp(dumpsysOutput string) (float64, error) {
	var lastValue string
	for _, line := range strings.Split(dumpsysOutput, "\n") {
		if !strings.Contains(line, "VIRTUAL-SKIN") {
			continue
		}
		if m := mValueRe.FindStringSubmatch(line); m != nil {
			lastValue = m[1]
		}
	}
	if lastValue == "" {
		return 0, fmt.Errorf("no VIRTUAL-SKIN reading found")
	}
	return strconv.ParseFloat(lastValue, 64)
}

// RealDownUpMbps samples the cellular rmnet byte counters twice across
// sampleWindow and computes an instantaneous rate - same idea as V1's
// speed_monitor.sh, reusing quota.CurrentRmnetBytes() rather than a
// second, separately-maintained byte-reading implementation.
//
// This only measures the cellular WAN leg specifically (rmnet*), same
// as V1 - not total device throughput across every interface.
func RealDownUpMbps(sampleWindow time.Duration) (down, up float64, err error) {
	rx1, tx1, err := rmnetRxTx()
	if err != nil {
		return 0, 0, err
	}
	time.Sleep(sampleWindow)
	rx2, tx2, err := rmnetRxTx()
	if err != nil {
		return 0, 0, err
	}

	seconds := sampleWindow.Seconds()
	downMbps := float64(rx2-rx1) * 8 / 1_000_000 / seconds
	upMbps := float64(tx2-tx1) * 8 / 1_000_000 / seconds
	if downMbps < 0 {
		downMbps = 0
	}
	if upMbps < 0 {
		upMbps = 0
	}
	return downMbps, upMbps, nil
}

// rmnetRxTx separates RX/TX (quota.CurrentRmnetBytes only returns the
// combined sum, which is enough for the quota cutoff but not for
// computing separate download/upload rates here).
func rmnetRxTx() (rx, tx int64, err error) {
	// Reuses the exact same /proc/net/dev parsing convention as
	// quota.CurrentRmnetBytes (see internal/quota/controller.go) -
	// duplicated rather than exported cross-package to keep quota's
	// public surface limited to what its own callers actually need.
	data, readErr := os.ReadFile("/proc/net/dev")
	if readErr != nil {
		return 0, 0, fmt.Errorf("open /proc/net/dev: %w", readErr)
	}
	re := regexp.MustCompile(`^\s*(rmnet\d+):\s*(.*)$`)
	for _, line := range strings.Split(string(data), "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fields := strings.Fields(m[2])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseInt(fields[0], 10, 64)
		t, _ := strconv.ParseInt(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx, nil
}
