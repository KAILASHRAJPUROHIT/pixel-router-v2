package hotspot

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	softApConfigPath = "/data/misc_ce/0/apexdata/com.android.wifi/WifiConfigStoreSoftAp.xml"
	hostapdConfDir   = "/data/vendor/wifi/hostapd"
	denyFile2Ghz     = "/data/vendor/wifi/hostapd/deny_2ghz.txt"
)

type Config struct {
	SSID         string
	Password     string
	Band         string   // e.g. "bridged_2_5"
	Deny2GhzMACs []string // devices pushed off the 2.4GHz radio (they roam to 5GHz on the same SSID)
}

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

// BringUp runs V1's full proven bring-up sequence: belt-and-suspenders
// disabling of the carrier's official tethering-entitlement path,
// stop before start (start-softap can silently no-op with a stale
// config otherwise), BandOptimization disabled while stopped, the
// actual start-softap call, then patching the freshly-regenerated
// hostapd confs for PMF and the 2.4GHz deny-ACL and reloading hostapd
// via SIGHUP (a SIGKILL here kills hostapd with no auto-restart -
// confirmed the hard way in V1).
func BringUp(cfg Config) error {
	if cfg.SSID == "" || cfg.Password == "" {
		return fmt.Errorf("SSID and password are required - refusing to start with a guessed/empty value")
	}

	runIgnoreErr("resetprop", "net.tethering.noprovisioning", "true")
	runIgnoreErr("pm", "disable", "com.google.android.tetheringentitlement")

	if err := run("cmd", "wifi", "stop-softap"); err != nil {
		return fmt.Errorf("stop-softap: %w", err)
	}
	time.Sleep(2 * time.Second)

	if xml, err := os.ReadFile(softApConfigPath); err == nil {
		patched := PatchBandOptimization(string(xml))
		os.WriteFile(softApConfigPath, []byte(patched), 0644)
	}

	if err := run("cmd", "wifi", "start-softap", cfg.SSID, "wpa2", cfg.Password, "-b", cfg.Band); err != nil {
		return fmt.Errorf("start-softap: %w", err)
	}

	time.Sleep(5 * time.Second)
	for _, name := range []string{"hostapd_wlan2.conf", "hostapd_wlan3.conf"} {
		path := hostapdConfDir + "/" + name
		if data, err := os.ReadFile(path); err == nil {
			os.WriteFile(path, []byte(PatchPMF(string(data))), 0644)
		}
	}

	if len(cfg.Deny2GhzMACs) > 0 {
		if err := os.WriteFile(denyFile2Ghz, []byte(BuildDenyList2Ghz(cfg.Deny2GhzMACs)), 0660); err != nil {
			return fmt.Errorf("write deny file: %w", err)
		}
		wlan2Conf := hostapdConfDir + "/hostapd_wlan2.conf"
		if data, err := os.ReadFile(wlan2Conf); err == nil {
			if !HasACLConfig(string(data)) {
				f, err := os.OpenFile(wlan2Conf, os.O_APPEND|os.O_WRONLY, 0644)
				if err == nil {
					f.WriteString(BuildACLAppend(denyFile2Ghz))
					f.Close()
				}
			}
		}
	}

	if pid, err := pgrepExact("hostapd"); err == nil && pid > 0 {
		exec.Command("kill", "-HUP", strconv.Itoa(pid)).Run()
	}

	return nil
}

func pgrepExact(name string) (int, error) {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("no matching process")
	}
	return strconv.Atoi(fields[0])
}

// IsUp reports whether either known AP interface name has a live IPv4
// address - the same check V1's watchdogs used (the AP can legitimately
// come up as either the bridged ap_br_wlan2 or plain wlan2 depending on
// bridging state).
func IsUp() bool {
	for _, iface := range []string{"ap_br_wlan2", "wlan2"} {
		if ifi, err := net.InterfaceByName(iface); err == nil {
			addrs, err := ifi.Addrs()
			if err == nil {
				for _, a := range addrs {
					if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.To4() != nil {
						return true
					}
				}
			}
		}
	}
	return false
}
