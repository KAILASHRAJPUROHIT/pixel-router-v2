package powergovernor

import (
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	cpuCount             = 8
	gpuPath              = "/sys/devices/platform/1c500000.mali"
	dreamStateFile       = "/data/data/com.router.aod/files/dream_state"
	dreamStateMaxAge     = 30 * time.Second
	thermalThrottledFile = "/data/adb/router_v2/state/thermal_throttled"
)

type RealController struct{}

func readFileTrim(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
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

func nearestStepAtOrBelow(available []int64, targetRaw, max int64) int64 {
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

func (RealController) SetCPUPct(pct int) error {
	for i := 0; i < cpuCount; i++ {
		base := "/sys/devices/system/cpu/cpu" + strconv.Itoa(i) + "/cpufreq"
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
		sort.Slice(available, func(a, b int) bool { return available[a] < available[b] })
		target := max
		if len(available) > 0 {
			target = nearestStepAtOrBelow(available, targetRaw, max)
		}
		os.WriteFile(base+"/scaling_max_freq", []byte(strconv.FormatInt(target, 10)), 0644)
	}
	return nil
}

func (RealController) SetGPUPct(pct int) error {
	maxStr, err := readFileTrim(gpuPath + "/max_freq")
	if err != nil || maxStr == "" {
		return nil
	}
	max, err := strconv.ParseInt(maxStr, 10, 64)
	if err != nil {
		return nil
	}
	targetRaw := max * int64(pct) / 100
	availStr, _ := readFileTrim(gpuPath + "/available_frequencies")
	available := parseFreqList(availStr)
	sort.Slice(available, func(a, b int) bool { return available[a] < available[b] })
	target := max
	if len(available) > 0 {
		target = nearestStepAtOrBelow(available, targetRaw, max)
	}
	os.WriteFile(gpuPath+"/scaling_max_freq", []byte(strconv.FormatInt(target, 10)), 0644)
	return nil
}

func (RealController) ThermallyThrottled() (bool, error) {
	v, err := readFileTrim(thermalThrottledFile)
	if err != nil {
		return false, nil // file missing - assume not throttled, matching V1's `cat ... || echo 0` default
	}
	return v == "1", nil
}

func (RealController) DreamState() (string, bool, error) {
	info, err := os.Stat(dreamStateFile)
	if err != nil {
		return "", false, nil
	}
	if time.Since(info.ModTime()) > dreamStateMaxAge {
		return "", false, nil
	}
	v, err := readFileTrim(dreamStateFile)
	if err != nil {
		return "", false, nil
	}
	return v, true, nil
}

func (RealController) Wakefulness() (string, error) {
	out, err := exec.Command("dumpsys", "power").CombinedOutput()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mWakefulness=") {
			return strings.TrimPrefix(line, "mWakefulness="), nil
		}
	}
	return "", nil
}
