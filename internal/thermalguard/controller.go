package thermalguard

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	cpuCount       = 8
	basePct        = 70 // permanent baseline set at boot by permanent_underclock.sh - never below this outside a SEVERE+ event
	baseGpuPct     = 60
	throttlePct    = 25 // lowest quarter of available steps, matching V1's (N+3)/4 index
	lowChargeUA    = "500000"
	gpuPath        = "/sys/devices/platform/1c500000.mali"
	gcpmCurrentMax = "/sys/devices/platform/google,cpm/power_supply/gcpm/current_max"
	gcpmCCCMax     = "/sys/devices/platform/google,cpm/power_supply/gcpm/constant_charge_current_max"
)

type RealController struct{}

func readFileTrim(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// nearestStepAtOrBelow mirrors V1's awk pipeline: given the available
// frequency steps and a target raw value, picks the largest available
// step that's <= target (falling back to max if none qualifies).
func nearestStepAtOrBelow(available []int64, targetRaw int64, max int64) int64 {
	best := int64(-1)
	for _, f := range available {
		if f <= targetRaw && f > best {
			best = f
		}
	}
	if best == -1 {
		return max
	}
	return best
}

func parseFreqList(s string) []int64 {
	fields := strings.Fields(s)
	out := make([]int64, 0, len(fields))
	for _, f := range fields {
		if v, err := strconv.ParseInt(f, 10, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func setCPUPct(pct int) {
	for i := 0; i < cpuCount; i++ {
		base := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq", i)
		maxStr, err := readFileTrim(base + "/cpuinfo_max_freq")
		if err != nil || maxStr == "" {
			continue
		}
		max, err := strconv.ParseInt(maxStr, 10, 64)
		if err != nil {
			continue
		}
		targetRaw := max * int64(pct) / 100
		availStr, _ := readFileTrim(base + "/scaling_available_frequencies")
		available := parseFreqList(availStr)
		target := max
		if len(available) > 0 {
			target = nearestStepAtOrBelow(available, targetRaw, max)
		}
		os.WriteFile(base+"/scaling_max_freq", []byte(strconv.FormatInt(target, 10)), 0644)
	}
}

// setGPUPctLowestQuarter mirrors V1's throttle_cpus()'s GPU handling,
// which used a fixed (N+3)/4 index into the sorted available list
// (the lowest quarter of steps) rather than a percent-of-max
// computation - kept as its own function since it's a different
// selection method than setCPUPct/setGPUPct's percent-based one.
func setGPULowestQuarter() {
	availStr, err := readFileTrim(gpuPath + "/available_frequencies")
	if err != nil || availStr == "" {
		return
	}
	available := parseFreqList(availStr)
	if len(available) == 0 {
		return
	}
	sort.Slice(available, func(i, j int) bool { return available[i] < available[j] })
	idx := (len(available) + 3) / 4
	if idx < 1 {
		idx = 1
	}
	if idx > len(available) {
		idx = len(available)
	}
	target := available[idx-1]
	os.WriteFile(gpuPath+"/scaling_max_freq", []byte(strconv.FormatInt(target, 10)), 0644)
}

func setGPUPct(pct int) {
	maxStr, err := readFileTrim(gpuPath + "/max_freq")
	if err != nil || maxStr == "" {
		return
	}
	max, err := strconv.ParseInt(maxStr, 10, 64)
	if err != nil {
		return
	}
	targetRaw := max * int64(pct) / 100
	availStr, _ := readFileTrim(gpuPath + "/available_frequencies")
	available := parseFreqList(availStr)
	target := max
	if len(available) > 0 {
		target = nearestStepAtOrBelow(available, targetRaw, max)
	}
	os.WriteFile(gpuPath+"/scaling_max_freq", []byte(strconv.FormatInt(target, 10)), 0644)
}

func (RealController) ThrottleCPUs() error {
	for i := 0; i < cpuCount; i++ {
		base := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq", i)
		availStr, err := readFileTrim(base + "/scaling_available_frequencies")
		if err != nil || availStr == "" {
			continue
		}
		available := parseFreqList(availStr)
		if len(available) == 0 {
			continue
		}
		sort.Slice(available, func(a, b int) bool { return available[a] < available[b] })
		idx := (len(available) + 3) / 4
		if idx < 1 {
			idx = 1
		}
		target := available[idx-1]
		os.WriteFile(base+"/scaling_max_freq", []byte(strconv.FormatInt(target, 10)), 0644)
	}
	setGPULowestQuarter()
	return nil
}

func (RealController) RestoreCPUs() error {
	setCPUPct(basePct)
	setGPUPct(baseGpuPct)
	return nil
}

func (RealController) BaselineFreqOK() (bool, error) {
	base := "/sys/devices/system/cpu/cpu0/cpufreq"
	maxStr, err := readFileTrim(base + "/cpuinfo_max_freq")
	if err != nil || maxStr == "" {
		return true, nil
	}
	curStr, err := readFileTrim(base + "/scaling_max_freq")
	if err != nil || curStr == "" {
		return true, nil
	}
	max, err1 := strconv.ParseInt(maxStr, 10, 64)
	cur, err2 := strconv.ParseInt(curStr, 10, 64)
	if err1 != nil || err2 != nil {
		return true, nil
	}
	targetRaw := max * basePct / 100
	availStr, _ := readFileTrim(base + "/scaling_available_frequencies")
	available := parseFreqList(availStr)
	target := max
	if len(available) > 0 {
		target = nearestStepAtOrBelow(available, targetRaw, max)
	}
	return cur == target, nil
}

var thermalStatusRe = regexp.MustCompile(`(?m)^Thermal Status:\s*(\d+)`)
var mValueRe = regexp.MustCompile(`mValue=([0-9.]+)`)

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

// SkinTempC scans line by line for the last VIRTUAL-SKIN entry,
// matching internal/aodstats and internal/dreamwatchdog's identical
// fix for the same bug (a single regex spanning the whole multi-line
// dumpsys output previously grabbed whichever unrelated sensor's
// Temperature{} block happened to come next, since mValue appears
// BEFORE mName on each real line).
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

func (RealController) BatteryPercent() (int, bool, error) {
	data, err := os.ReadFile("/sys/class/power_supply/battery/capacity")
	if err != nil {
		return 0, false, fmt.Errorf("read battery capacity: %w", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

func (RealController) LimitChargeCurrent() error {
	os.WriteFile(gcpmCurrentMax, []byte(lowChargeUA), 0644)
	os.WriteFile(gcpmCCCMax, []byte(lowChargeUA), 0644)
	return nil
}

// RestoreChargeCurrent mirrors V1's restore_charge_current, which
// remembered the pre-clamp values the first time it clamped (a
// battery_charge_limit.sh-set 80%-bypass-charge default of 3000000uA
// if nothing was ever saved) and restored those specific values -
// simplified here to just restoring the same well-known default,
// since that's what V1's fallback already was in the common case
// (defaults file didn't exist yet) and avoids needing our own
// separate persisted-defaults file for a value that's already fixed
// at boot by battery_charge_limit.sh anyway.
func (RealController) RestoreChargeCurrent() error {
	os.WriteFile(gcpmCurrentMax, []byte("3000000"), 0644)
	os.WriteFile(gcpmCCCMax, []byte("0"), 0644)
	return nil
}
