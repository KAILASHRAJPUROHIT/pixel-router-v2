// Package blocklist enforces a per-MAC device block via a plain
// iptables FORWARD DROP rule, ported from V1's dashboard "Block"/
// "Allow" buttons. This targets iptables directly rather than
// hostapd's ACL (deny_mac_file, already used elsewhere in
// internal/hotspot for 2.4GHz band-steering) because hostapd
// regenerates its own config on every start-softap - see
// internal/hotspot's HasACLConfig doc comment - which would silently
// wipe a block on every hotspot restart. A FORWARD-chain rule is a
// separate netfilter subsystem untouched by softap toggling, so it
// survives hotspot restarts; it's only lost on a full device reboot,
// which is why the store persists the intent separately (see
// store.SetDeviceBlocked) for re-assertion at next boot.
package blocklist

import "os/exec"

// BlockArgs/UnblockArgs/CheckArgs build the iptables argv for a given
// MAC - split out from the exec calls themselves so they're
// unit-testable without root or a real iptables binary.
func BlockArgs(mac string) []string {
	return []string{"-I", "FORWARD", "1", "-m", "mac", "--mac-source", mac, "-j", "DROP"}
}

func UnblockArgs(mac string) []string {
	return []string{"-D", "FORWARD", "-m", "mac", "--mac-source", mac, "-j", "DROP"}
}

func CheckArgs(mac string) []string {
	return []string{"-C", "FORWARD", "-m", "mac", "--mac-source", mac, "-j", "DROP"}
}

type Controller interface {
	IsBlocked(mac string) bool
	Block(mac string) error
	Unblock(mac string) error
}

type RealController struct{}

func (RealController) IsBlocked(mac string) bool {
	return exec.Command("iptables", CheckArgs(mac)...).Run() == nil
}

// Block is idempotent - safe to call even if the rule is already
// present (e.g. re-asserting every already-blocked device at startup).
func (c RealController) Block(mac string) error {
	if c.IsBlocked(mac) {
		return nil
	}
	return exec.Command("iptables", BlockArgs(mac)...).Run()
}

func (RealController) Unblock(mac string) error {
	return exec.Command("iptables", UnblockArgs(mac)...).Run()
}
