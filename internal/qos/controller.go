package qos

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type RealController struct{}

func tcBinary() (string, error) {
	if err := exec.Command("tc", "-h").Run(); err == nil {
		return "tc", nil
	}
	if err := exec.Command("/data/adb/magisk/busybox", "tc", "-h").Run(); err == nil {
		return "/data/adb/magisk/busybox tc", nil
	}
	return "", fmt.Errorf("no usable tc binary found on this device")
}

func runTC(args ...string) error {
	tc, err := tcBinary()
	if err != nil {
		return err
	}
	parts := strings.Fields(tc)
	full := append(append([]string{}, parts[1:]...), args...)
	out, err := exec.Command(parts[0], full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tc %v: %w (%s)", args, err, string(out))
	}
	return nil
}

func runTCIgnoreErr(args ...string) {
	tc, err := tcBinary()
	if err != nil {
		return
	}
	parts := strings.Fields(tc)
	full := append(append([]string{}, parts[1:]...), args...)
	exec.Command(parts[0], full...).Run()
}

func (RealController) DetectWanInterface() (string, error) {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) && strings.HasPrefix(fields[i+1], "rmnet") {
				return fields[i+1], nil
			}
		}
	}
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return "", err
	}
	best, bestTotal := "", int64(-1)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "rmnet") {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		if rx+tx > bestTotal {
			best, bestTotal = parts[0], rx+tx
		}
	}
	return best, nil
}

func (RealController) PriorityDeviceIP(mac string) (string, bool, error) {
	out, err := exec.Command("ip", "neigh", "show").CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("ip neigh show: %w", err)
	}
	macLower := strings.ToLower(mac)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(strings.ToLower(line), macLower) || !strings.Contains(line, "lladdr") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[0], true, nil
		}
	}
	return "", false, nil
}

func (RealController) QdiscPresent(iface string) (bool, error) {
	tc, err := tcBinary()
	if err != nil {
		return false, err
	}
	parts := strings.Fields(tc)
	full := append(append([]string{}, parts[1:]...), "qdisc", "show", "dev", iface)
	out, err := exec.Command(parts[0], full...).CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "qdisc prio 1: root"), nil
}

func (RealController) ApplyQoS(iface, priorityIP string) error {
	runTCIgnoreErr("qdisc", "del", "dev", iface, "root")

	if err := runTC("qdisc", "add", "dev", iface, "root", "handle", "1:", "prio", "bands", "3",
		"priomap", "1", "2", "2", "2", "1", "2", "0", "0", "1", "1", "1", "1", "1", "1", "1", "1"); err != nil {
		return err
	}
	if err := runTC("qdisc", "add", "dev", iface, "parent", "1:1", "handle", "10:", "fq_codel"); err != nil {
		return err
	}
	if err := runTC("qdisc", "add", "dev", iface, "parent", "1:2", "handle", "20:", "fq_codel"); err != nil {
		return err
	}
	if err := runTC("qdisc", "add", "dev", iface, "parent", "1:3", "handle", "30:", "fq_codel"); err != nil {
		return err
	}
	if err := runTC("filter", "add", "dev", iface, "protocol", "ip", "parent", "1:0", "prio", "1",
		"u32", "match", "ip", "src", priorityIP, "flowid", "1:1"); err != nil {
		return err
	}
	return runTC("filter", "add", "dev", iface, "protocol", "ip", "parent", "1:0", "prio", "1",
		"u32", "match", "ip", "dst", priorityIP, "flowid", "1:1")
}
