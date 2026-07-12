//go:build linux

package ttlproxy

import (
	"fmt"
	"os/exec"
)

// SetupConfig mirrors V1's ttl_proxy.sh wiring, extended to cover IPv6
// alongside the original IPv4 path - the gap this research/build pass
// exists to close. Confirmed live on the V1 device: IPv6 forwarding
// was enabled, the hotspot bridge had a real global IPv6 address, and
// IPv6 traffic was actively forwarding with zero hop-limit
// normalization - V1's C ttlproxy never touched IPv6 at all.
type SetupConfig struct {
	ProxyPort  int
	Mark       string
	TableID    string
	Interfaces []string // e.g. wlan2, ncm0 - the hotspot/tether-facing interfaces
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", name, args, err, string(out))
	}
	return nil
}

// runIgnoreErr is for idempotent cleanup steps (deleting a rule that
// may not exist yet on first run) - same pattern V1 used throughout.
func runIgnoreErr(name string, args ...string) {
	exec.Command(name, args...).Run()
}

// SetupIPv4 wires the same iptables+policy-routing TPROXY plumbing
// V1's ttl_proxy.sh used: a TTLPROXY chain in the mangle table, TPROXY
// target on ProxyPort, hooked into PREROUTING for each hotspot/tether
// interface, plus the fwmark policy-routing rule that makes the kernel
// actually deliver marked packets to the local TPROXY socket.
func SetupIPv4(cfg SetupConfig) error {
	runIgnoreErr("ip", "rule", "del", "fwmark", cfg.Mark, "lookup", cfg.TableID)
	if err := run("ip", "rule", "add", "fwmark", cfg.Mark, "lookup", cfg.TableID); err != nil {
		return err
	}
	runIgnoreErr("ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", cfg.TableID)
	if err := run("ip", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", cfg.TableID); err != nil {
		return err
	}

	// Jump rules referencing the chain must be removed before the chain
	// itself can be deleted (-X refuses to delete a still-referenced
	// chain) - doing this in the opposite order left a stale chain
	// behind on every restart, since the ignored -X failure meant -N
	// then hit "Chain already exists" forever.
	for _, iface := range cfg.Interfaces {
		runIgnoreErr("iptables", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", "TTLPROXYV2")
	}
	runIgnoreErr("iptables", "-t", "mangle", "-F", "TTLPROXYV2")
	runIgnoreErr("iptables", "-t", "mangle", "-X", "TTLPROXYV2")
	if err := run("iptables", "-t", "mangle", "-N", "TTLPROXYV2"); err != nil {
		return err
	}
	if err := run("iptables", "-t", "mangle", "-A", "TTLPROXYV2", "-p", "tcp",
		"-j", "TPROXY", "--on-port", fmt.Sprint(cfg.ProxyPort), "--tproxy-mark", cfg.Mark); err != nil {
		return err
	}
	for _, iface := range cfg.Interfaces {
		if err := run("iptables", "-t", "mangle", "-A", "PREROUTING", "-i", iface, "-p", "tcp", "-j", "TTLPROXYV2"); err != nil {
			return err
		}
	}
	return nil
}

// SetupIPv6 is the IPv6 equivalent using ip6tables and a separate
// TTLPROXYV2_6 chain/table - this whole function is the piece that did
// not exist anywhere in V1.
func SetupIPv6(cfg SetupConfig) error {
	runIgnoreErr("ip", "-6", "rule", "del", "fwmark", cfg.Mark, "lookup", cfg.TableID)
	if err := run("ip", "-6", "rule", "add", "fwmark", cfg.Mark, "lookup", cfg.TableID); err != nil {
		return err
	}
	runIgnoreErr("ip", "-6", "route", "del", "local", "::/0", "dev", "lo", "table", cfg.TableID)
	if err := run("ip", "-6", "route", "add", "local", "::/0", "dev", "lo", "table", cfg.TableID); err != nil {
		return err
	}

	for _, iface := range cfg.Interfaces {
		runIgnoreErr("ip6tables", "-t", "mangle", "-D", "PREROUTING", "-i", iface, "-p", "tcp", "-j", "TTLPROXYV2_6")
	}
	runIgnoreErr("ip6tables", "-t", "mangle", "-F", "TTLPROXYV2_6")
	runIgnoreErr("ip6tables", "-t", "mangle", "-X", "TTLPROXYV2_6")
	if err := run("ip6tables", "-t", "mangle", "-N", "TTLPROXYV2_6"); err != nil {
		return err
	}
	if err := run("ip6tables", "-t", "mangle", "-A", "TTLPROXYV2_6", "-p", "tcp",
		"-j", "TPROXY", "--on-port", fmt.Sprint(cfg.ProxyPort), "--tproxy-mark", cfg.Mark); err != nil {
		return err
	}
	for _, iface := range cfg.Interfaces {
		if err := run("ip6tables", "-t", "mangle", "-A", "PREROUTING", "-i", iface, "-p", "tcp", "-j", "TTLPROXYV2_6"); err != nil {
			return err
		}
	}
	return nil
}
