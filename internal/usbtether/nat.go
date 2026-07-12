package usbtether

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Confirmed live 2026-07-12: after a real reboot, ncm0 came up
// successfully (IsUp() true, valid IP, IP forwarding enabled) but
// Android's own tethering framework never registered a MASQUERADE/
// FORWARD rule set for it - EnsureUp only ever verified interface
// state (matching V1's own "trust interface state over the API's
// return code" philosophy), which turned out to prove less than
// assumed: the interface can come up via the kernel USB gadget layer
// alone, independent of whether TetheringManager's NAT setup actually
// completed. Result: tethered clients had a live ncm0 link with zero
// route to the actual internet. Rather than depend on that opaque,
// sometimes-incomplete Android API to also handle NAT, this package
// now asserts the NAT/FORWARD rules itself, idempotently, every check
// cycle - the same "don't trust it, verify and assert" pattern used
// throughout this codebase (hotspot's PMF/ACL patching, ttlproxy's
// chain setup, etc).

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", name, args, err, string(out))
	}
	return nil
}

// DetectWanInterface finds the current cellular WAN interface, same
// convention used in internal/qos and internal/failover: prefer the
// default route's own device if it's an rmnet interface, falling back
// to whichever rmnet* interface has moved the most bytes.
func DetectWanInterface() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) && strings.HasPrefix(fields[i+1], "rmnet") {
				return fields[i+1]
			}
		}
	}
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return ""
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
	return best
}

// NATRulesPresent reports whether the MASQUERADE + bidirectional
// FORWARD rules for ncm0<->wan already exist.
func NATRulesPresent(wan string) bool {
	if wan == "" {
		return false
	}
	if exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-o", wan, "-j", "MASQUERADE").Run() != nil {
		return false
	}
	if exec.Command("iptables", "-C", "FORWARD", "-i", "ncm0", "-o", wan, "-j", "ACCEPT").Run() != nil {
		return false
	}
	if exec.Command("iptables", "-C", "FORWARD", "-i", wan, "-o", "ncm0", "-j", "ACCEPT").Run() != nil {
		return false
	}
	return true
}

// AssertNATRules idempotently adds any missing rule, returning whether
// it had to add anything.
func AssertNATRules(wan string) (bool, error) {
	if wan == "" {
		return false, fmt.Errorf("no WAN interface detected")
	}
	changed := false
	if exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-o", wan, "-j", "MASQUERADE").Run() != nil {
		if err := run("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", wan, "-j", "MASQUERADE"); err != nil {
			return changed, err
		}
		changed = true
	}
	if exec.Command("iptables", "-C", "FORWARD", "-i", "ncm0", "-o", wan, "-j", "ACCEPT").Run() != nil {
		if err := run("iptables", "-I", "FORWARD", "1", "-i", "ncm0", "-o", wan, "-j", "ACCEPT"); err != nil {
			return changed, err
		}
		changed = true
	}
	if exec.Command("iptables", "-C", "FORWARD", "-i", wan, "-o", "ncm0", "-j", "ACCEPT").Run() != nil {
		if err := run("iptables", "-I", "FORWARD", "1", "-i", wan, "-o", "ncm0", "-j", "ACCEPT"); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}
