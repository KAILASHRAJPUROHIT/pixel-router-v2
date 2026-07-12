package devicescan

import (
	"os/exec"
	"regexp"
	"strings"
)

// RealController scans the same interfaces V1's update.sh did: the
// hotspot AP interface (whichever of ap_br_wlan2/wlan2 exists) and the
// USB tether interface, so a device tethered by cable is tracked too,
// not just WiFi hotspot clients.
type RealController struct {
	Interfaces []string
}

func NewRealController() RealController {
	return RealController{Interfaces: []string{"ap_br_wlan2", "wlan2", "ncm0"}}
}

func (c RealController) ListLiveClients() ([]Client, error) {
	var all []Client
	for _, iface := range c.Interfaces {
		out, err := exec.Command("ip", "neigh", "show", "dev", iface).CombinedOutput()
		if err != nil {
			// Interface doesn't exist right now (e.g. USB tether not
			// plugged in) - not an error, just nothing to report from it.
			continue
		}
		all = append(all, parseLiveClients(string(out), iface)...)
	}
	return dedupeByMAC(all), nil
}

var neighLineRe = regexp.MustCompile(`lladdr\s+(\S+)\s+(\S+)\s*$`)

// liveStates mirrors V1's update.sh grep -E " (REACHABLE|DELAY|PROBE)$"
// - STALE/FAILED/INCOMPLETE entries are excluded since they don't
// reflect a currently-connected device (STALE in particular can linger
// long after a device roamed away or went to sleep).
var liveStates = map[string]bool{"REACHABLE": true, "DELAY": true, "PROBE": true}

// parseLiveClients is split out from ListLiveClients purely so the
// actual parsing/filtering logic is unit-testable without a device.
func parseLiveClients(ipNeighOutput, band string) []Client {
	var clients []Client
	for _, line := range strings.Split(ipNeighOutput, "\n") {
		m := neighLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		mac, state := strings.ToLower(m[1]), m[2]
		if !liveStates[state] {
			continue
		}
		clients = append(clients, Client{MAC: mac, Band: band})
	}
	return clients
}

// dedupeByMAC keeps the first Band seen per MAC - a device can appear
// on multiple rows (IPv4 + IPv6 + link-local) or, in principle, on more
// than one scanned interface; one row per real device is what the
// device-count stat and the devices table both want.
func dedupeByMAC(clients []Client) []Client {
	seen := make(map[string]bool, len(clients))
	out := make([]Client, 0, len(clients))
	for _, c := range clients {
		if seen[c.MAC] {
			continue
		}
		seen[c.MAC] = true
		out = append(out, c)
	}
	return out
}
