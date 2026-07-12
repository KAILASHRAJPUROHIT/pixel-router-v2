package vpntether

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	fwmark           = "0x9999"
	table            = "90"
	wgReceiver       = "com.wireguard.android/.model.TunnelManager$IntentReceiver"
	wgTunnelName     = "pxlvpn"
	internetTypePath = "/data/adb/router_v2/state/failover_internet_type.txt"

	apBridgeIface           = "ap_br_wlan2"
	localSubnetRulePriority = "50"
)

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

func (RealController) ReadInternetType() (string, error) {
	data, err := os.ReadFile(internetTypePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (RealController) TunnelRxBytes() (int64, bool, error) {
	data, err := os.ReadFile("/sys/class/net/tun0/statistics/rx_bytes")
	if err != nil {
		return 0, false, nil // tun0 doesn't exist - not an error, just no reading
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

// apInterfaceCandidates is a superset covering every topology this
// device's hotspot can be in: ap_br_wlan2 (the bridge device, present
// in dual-band mode) and the two raw physical radios (wlan2=2.4GHz,
// wlan3=5GHz, used directly with no bridge in single-band-only mode).
// Confirmed live 2026-07-12 as a real, serious bug: this device's
// kernel has no br_netfilter module (no
// /proc/sys/net/bridge/bridge-nf-call-iptables at all), so iptables
// structurally cannot see bridged client traffic via the physical
// port names - a rule matching only `-i wlan2` never matched a single
// packet of real bridged traffic (0 hits after live traffic generation,
// vs an immediate 62-packet match on `-i ap_br_wlan2`), meaning this
// Airtel-tethering-detection bypass had silently never activated for
// any WiFi client's traffic. Asserting rules for every candidate is
// harmless for whichever names don't currently exist - they just match
// zero packets - and avoids needing to detect the active band mode or
// restart this service whenever it changes.
var apInterfaceCandidates = []string{"ap_br_wlan2", "wlan2", "wlan3"}

func apInterfaceExists() bool {
	for _, iface := range apInterfaceCandidates {
		if _, err := os.Stat("/sys/class/net/" + iface); err == nil {
			return true
		}
	}
	return false
}

func (RealController) ApplyBypassRouting() error {
	if !apInterfaceExists() {
		return fmt.Errorf("no AP interface present")
	}
	ruleOut, _ := exec.Command("ip", "rule", "show").Output()
	if !strings.Contains(string(ruleOut), "fwmark "+fwmark+" lookup "+table) {
		runIgnoreErr("ip", "rule", "add", "fwmark", fwmark, "table", table, "priority", "100")
	}
	runIgnoreErr("ip", "route", "replace", "default", "dev", "tun0", "table", table)

	for _, iface := range apInterfaceCandidates {
		if run("iptables", "-t", "mangle", "-C", "PREROUTING", "-i", iface, "-j", "MARK", "--set-mark", fwmark) != nil {
			if err := run("iptables", "-t", "mangle", "-A", "PREROUTING", "-i", iface, "-j", "MARK", "--set-mark", fwmark); err != nil {
				return err
			}
		}
		if run("iptables", "-C", "FORWARD", "-i", iface, "-o", "tun0", "-j", "ACCEPT") != nil {
			if err := run("iptables", "-I", "FORWARD", "1", "-i", iface, "-o", "tun0", "-j", "ACCEPT"); err != nil {
				return err
			}
		}
		if run("iptables", "-C", "FORWARD", "-i", "tun0", "-o", iface, "-j", "ACCEPT") != nil {
			if err := run("iptables", "-I", "FORWARD", "1", "-i", "tun0", "-o", iface, "-j", "ACCEPT"); err != nil {
				return err
			}
		}
	}

	if run("iptables", "-t", "nat", "-C", "POSTROUTING", "-o", "tun0", "-j", "MASQUERADE") != nil {
		if err := run("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "tun0", "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	return nil
}

func (RealController) RemoveBypassRouting() error {
	for _, iface := range apInterfaceCandidates {
		runIgnoreErr("iptables", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-j", "MARK", "--set-mark", fwmark)
		runIgnoreErr("iptables", "-D", "FORWARD", "-i", iface, "-o", "tun0", "-j", "ACCEPT")
		runIgnoreErr("iptables", "-D", "FORWARD", "-i", "tun0", "-o", iface, "-j", "ACCEPT")
	}
	runIgnoreErr("ip", "rule", "del", "fwmark", fwmark, "table", table)
	runIgnoreErr("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", "tun0", "-j", "MASQUERADE")
	return nil
}

// EnsureLocalSubnetRoute asserts a rule that keeps the router's own
// outbound connections to its hotspot clients off the WireGuard tunnel,
// regardless of whether this package's own wlan2 bypass marking is
// currently active - see the Controller interface's doc comment for
// why this is needed independently of that. Priority 50 is well ahead
// of the WireGuard app's own default-route rule (observed at priority
// 13000 live 2026-07-12), so a directly-connected hotspot-client
// destination is resolved via the main table (which already has the
// kernel-installed connected route for that subnet) before the
// tunnel-forcing rule is ever consulted.
func (RealController) EnsureLocalSubnetRoute() error {
	out, err := exec.Command("ip", "route", "show", "dev", apBridgeIface, "scope", "link").Output()
	if err != nil {
		return nil // AP interface not up yet - nothing to protect
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return nil
	}
	subnet := fields[0]

	ruleOut, _ := exec.Command("ip", "rule", "show").Output()
	if !strings.Contains(string(ruleOut), "to "+subnet+" lookup main") {
		runIgnoreErr("ip", "rule", "add", "to", subnet, "lookup", "main", "priority", localSubnetRulePriority)
	}
	return nil
}

func (RealController) ReconnectTunnel() error {
	runIgnoreErr("am", "broadcast", "-n", wgReceiver, "-a", "com.wireguard.android.action.SET_TUNNEL_DOWN", "--es", "tunnel", wgTunnelName)
	time.Sleep(3 * time.Second)
	runIgnoreErr("am", "broadcast", "-n", wgReceiver, "-a", "com.wireguard.android.action.SET_TUNNEL_UP", "--es", "tunnel", wgTunnelName)
	return nil
}
