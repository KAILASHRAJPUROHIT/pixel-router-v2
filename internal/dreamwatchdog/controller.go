package dreamwatchdog

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Controller is everything both loops need from the outside world -
// RealController shells out to the same settings/dumpsys/pm/cmd
// commands V1's two shell scripts used; a fake in tests exercises the
// decision logic (mainly ThermalGate and the recovery backoff/deep-
// check state machine) without a device.
type Controller interface {
	// Readings, used by both loops.
	SkinTempC() (tempC float64, hasReading bool, err error)
	ThermalStatus() (status int, hasReading bool, err error)

	// Fast loop (mirrors aod_dream_recovery.sh).
	Wakefulness() (string, error)
	CurrentDreamIsOurs() (bool, error)
	StartDreaming() error
	IsRouterAodPackageEnabled() (bool, error)
	IsUserLocked() (bool, error)
	// IsCharging reports live charging state. hasReading=false on a
	// read failure - treated as "assume charging" by the caller (fail
	// open, so a transient dumpsys hiccup can't silently disable AOD).
	IsCharging() (charging bool, hasReading bool, err error)

	// Slow loop (mirrors aod_watchdog.sh's settings/component drift
	// correction). Each Assert method checks-and-fixes in one call and
	// reports whether it changed anything, for logging.
	AssertScreensaverEnabled() (changed bool, err error)
	AssertScreensaverDefaultComponent() (changed bool, err error)
	AssertPackageVerifierEnabled() (changed bool, err error)
	AssertRouterAodPackageEnabled() (changed bool, err error)
	AssertAdbWifiDisabled() (changed bool, err error)
	AssertDozeSettingDisabled() (changed bool, err error)
	AssertNfcDisabled() (changed bool, err error)
	AssertFixedBrightness() (changed bool, err error)
	AssertLowLightClockDisabled() (changed bool, err error)
	AssertDozeServiceDisabled() (changed bool, err error)
	// Escalation path used when the fast loop sees sustained
	// mismatches (mirrors aod_dream_recovery.sh's "deep check").
	ForceRestartRouterAod() error
}

const routerAodPackage = "com.router.aod"
const routerAodDreamComponent = routerAodPackage + "/.RouterDreamService"

type RealController struct{}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", name, args, err, string(out))
	}
	return nil
}

func runIgnoreErr(name string, args ...string) {
	exec.Command(name, args...).Run()
}

func settingsGet(namespace, key string) (string, error) {
	out, err := exec.Command("settings", "get", namespace, key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var mValueRe = regexp.MustCompile(`mValue=([0-9.]+)`)
var thermalStatusRe = regexp.MustCompile(`(?m)^Thermal Status:\s*(\d+)`)

// SkinTempC scans dumpsys thermalservice line by line for the last
// entry containing "VIRTUAL-SKIN", extracting mValue from that SAME
// line. Confirmed live 2026-07-12 that an earlier single-regex version
// spanning the whole (multi-line) output searched for "VIRTUAL-SKIN"
// and then the NEXT "mValue=" after it - but each real entry actually
// reads "Temperature{mValue=X, mType=N, mName=VIRTUAL-SKIN,
// mStatus=S}" (mValue BEFORE mName on the same line), so that older
// version always skipped past the real reading and grabbed whatever
// unrelated sensor's block happened to come next in the dump (caught
// live grabbing usb_pwr_therm's 45.7C instead of the real 41.0C skin
// reading - which alone was enough to spuriously trigger dreamwatchdog's
// thermal-safety pause). This mirrors internal/aodstats's identical fix
// and V1's own working `grep "Temperature{.*VIRTUAL-SKIN"` approach.
func (RealController) SkinTempC() (float64, bool, error) {
	out, err := exec.Command("dumpsys", "thermalservice").CombinedOutput()
	if err != nil {
		return 0, false, fmt.Errorf("dumpsys thermalservice: %w", err)
	}
	var lastValue string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "VIRTUAL-SKIN") {
			continue
		}
		if m := mValueRe.FindStringSubmatch(line); m != nil {
			lastValue = m[1]
		}
	}
	if lastValue == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(lastValue, 64)
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

func (RealController) ThermalStatus() (int, bool, error) {
	out, err := exec.Command("dumpsys", "thermalservice").CombinedOutput()
	if err != nil {
		return 0, false, fmt.Errorf("dumpsys thermalservice: %w", err)
	}
	m := thermalStatusRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0, false, nil
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

func (RealController) Wakefulness() (string, error) {
	out, err := exec.Command("dumpsys", "power").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("dumpsys power: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mWakefulness=") {
			return strings.TrimPrefix(line, "mWakefulness="), nil
		}
	}
	return "", nil
}

func (RealController) CurrentDreamIsOurs() (bool, error) {
	out, err := exec.Command("dumpsys", "dreams").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys dreams: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mCurrentDream=") {
			return strings.Contains(line, routerAodPackage), nil
		}
	}
	return false, nil
}

func (RealController) StartDreaming() error {
	return run("cmd", "dreams", "start-dreaming")
}

func (RealController) IsRouterAodPackageEnabled() (bool, error) {
	out, err := exec.Command("dumpsys", "package", routerAodPackage).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys package %s: %w", routerAodPackage, err)
	}
	return strings.Contains(string(out), "enabled=1"), nil
}

func (RealController) IsUserLocked() (bool, error) {
	out, err := exec.Command("dumpsys", "user").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys user: %w", err)
	}
	return strings.Contains(string(out), "RUNNING_LOCKED"), nil
}

var batteryStatusRe = regexp.MustCompile(`(?m)^\s*status:\s*(\d+)`)

// IsCharging mirrors V1's update.sh BATT_STATUS_CODE check: Android's
// BatteryManager status 2=charging, 5=full - anything else (commonly 4
// "not charging" here) counts as not charging.
func (RealController) IsCharging() (bool, bool, error) {
	out, err := exec.Command("dumpsys", "battery").CombinedOutput()
	if err != nil {
		return false, false, fmt.Errorf("dumpsys battery: %w", err)
	}
	m := batteryStatusRe.FindStringSubmatch(string(out))
	if m == nil {
		return false, false, nil
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return false, false, nil
	}
	return code == 2 || code == 5, true, nil
}

func (RealController) ForceRestartRouterAod() error {
	runIgnoreErr("am", "force-stop", routerAodPackage)
	if err := run("settings", "put", "secure", "screensaver_enabled", "0"); err != nil {
		return err
	}
	if err := run("settings", "put", "secure", "screensaver_enabled", "1"); err != nil {
		return err
	}
	return run("input", "keyevent", "KEYCODE_WAKEUP")
}

func (RealController) AssertScreensaverEnabled() (bool, error) {
	v, err := settingsGet("secure", "screensaver_enabled")
	if err != nil || v == "1" {
		return false, err
	}
	return true, run("settings", "put", "secure", "screensaver_enabled", "1")
}

func (RealController) AssertScreensaverDefaultComponent() (bool, error) {
	v, err := settingsGet("secure", "screensaver_default_component")
	if err != nil || v == routerAodDreamComponent {
		return false, err
	}
	return true, run("settings", "put", "secure", "screensaver_default_component", routerAodDreamComponent)
}

func (RealController) AssertPackageVerifierEnabled() (bool, error) {
	v, _ := settingsGet("global", "package_verifier_enable")
	verifierOut, _ := exec.Command("dumpsys", "package", "com.google.android.verifier").CombinedOutput()
	verifierEnabled := strings.Contains(string(verifierOut), "enabled=1")
	if v == "1" && verifierEnabled {
		return false, nil
	}
	runIgnoreErr("pm", "enable", "com.google.android.verifier")
	return true, run("settings", "put", "global", "package_verifier_enable", "1")
}

func (RealController) AssertRouterAodPackageEnabled() (bool, error) {
	enabled, err := RealController{}.IsRouterAodPackageEnabled()
	if err != nil || enabled {
		return false, err
	}
	return true, run("pm", "enable", routerAodPackage)
}

func (RealController) AssertAdbWifiDisabled() (bool, error) {
	v, err := settingsGet("global", "adb_wifi_enabled")
	if err != nil || v == "0" {
		return false, err
	}
	return true, run("settings", "put", "global", "adb_wifi_enabled", "0")
}

func (RealController) AssertDozeSettingDisabled() (bool, error) {
	v, err := settingsGet("secure", "doze_enabled")
	if err != nil || v == "0" {
		return false, err
	}
	return true, run("settings", "put", "secure", "doze_enabled", "0")
}

func (RealController) AssertNfcDisabled() (bool, error) {
	out, err := exec.Command("dumpsys", "nfc").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys nfc: %w", err)
	}
	state := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mState=") {
			state = strings.TrimPrefix(line, "mState=")
			break
		}
	}
	if state == "off" {
		return false, nil
	}
	return true, run("svc", "nfc", "disable")
}

func (RealController) AssertFixedBrightness() (bool, error) {
	v, err := settingsGet("system", "screen_brightness_mode")
	if err != nil || v == "0" {
		return false, err
	}
	return true, run("settings", "put", "system", "screen_brightness_mode", "0")
}

func (RealController) AssertLowLightClockDisabled() (bool, error) {
	return assertComponentDisabled("com.android.systemui/.lowlightclock.LowLightClockDreamService")
}

func (RealController) AssertDozeServiceDisabled() (bool, error) {
	return assertComponentDisabled("com.android.systemui/.doze.DozeService")
}

// assertComponentDisabled checks whether component already appears in
// com.android.systemui's disabledComponents block and pm-disables it
// if not. The "already disabled" check is only a log-noise/redundant-
// call optimization (pm disable is idempotent), so a simple proximity
// check - does the component name appear within the ~6 lines
// following the "disabledComponents" header - is good enough; it
// doesn't need to be a precise section parser.
func assertComponentDisabled(component string) (bool, error) {
	out, err := exec.Command("dumpsys", "package", "com.android.systemui").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys package com.android.systemui: %w", err)
	}
	name := component[strings.LastIndex(component, "/")+1:]
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "disabledComponents") {
			continue
		}
		end := i + 6
		if end > len(lines) {
			end = len(lines)
		}
		for _, follow := range lines[i:end] {
			if strings.Contains(follow, name) {
				return false, nil
			}
		}
		break
	}
	return true, run("pm", "disable", component)
}
