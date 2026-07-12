package connmonitor

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type RealController struct{}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", name, args, err, string(out))
	}
	return nil
}

func settingsGet(namespace, key string) (string, error) {
	out, err := exec.Command("settings", "get", namespace, key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// --- Detection safety ---

func (RealController) TTLProxyRulesIntact() (bool, error) {
	if exec.Command("sh", "-c", "ps -ef | grep -F ttlproxy | grep -v grep").Run() != nil {
		return false, nil
	}
	if run("iptables", "-t", "mangle", "-C", "PREROUTING", "-i", "wlan2", "-p", "tcp", "-j", "TTLPROXYV2") != nil {
		return false, nil
	}
	if run("iptables", "-t", "mangle", "-C", "PREROUTING", "-i", "ncm0", "-p", "tcp", "-j", "TTLPROXYV2") != nil {
		return false, nil
	}
	out, err := exec.Command("iptables", "-t", "mangle", "-L", "TTLPROXYV2", "-n").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "TPROXY") {
		return false, nil
	}
	return true, nil
}

// RestartTTLProxy kills V2's ttlproxy by pattern (its own binary name
// is unambiguous, unlike the shell-script pkill hazards found
// elsewhere this session) - the supervisor's restart:always policy
// relaunches it immediately, re-running its own SetupIPv4/SetupIPv6
// from scratch.
func (RealController) RestartTTLProxy() error {
	exec.Command("pkill", "-f", "/data/adb/router_v2/bin/ttlproxy").Run()
	return nil
}

const hoppatchDisableFlag = "/data/adb/modules/hoppatch/disable"

func (RealController) HoppatchDisableFlagPresent() (bool, error) {
	_, err := os.Stat(hoppatchDisableFlag)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (RealController) CreateHoppatchDisableFlag() error {
	if err := os.MkdirAll("/data/adb/modules/hoppatch", 0755); err != nil {
		return err
	}
	f, err := os.Create(hoppatchDisableFlag)
	if err != nil {
		return err
	}
	return f.Close()
}

// --- Stability / signal ---

func (RealController) PingStats(target string, count int) (int, float64, bool, error) {
	out, err := exec.Command("ping", "-c", strconv.Itoa(count), "-W", "2", target).CombinedOutput()
	text := string(out)
	if err != nil && text == "" {
		return 0, 0, false, fmt.Errorf("ping: %w", err)
	}

	lossRe := regexp.MustCompile(`(\d+)% packet loss`)
	lossPct := -1
	if m := lossRe.FindStringSubmatch(text); m != nil {
		lossPct, _ = strconv.Atoi(m[1])
	}

	rttRe := regexp.MustCompile(`rtt [a-z/]+ = ([0-9.]+)/([0-9.]+)/([0-9.]+)`)
	avgRTT := 0.0
	hasRTT := false
	if m := rttRe.FindStringSubmatch(text); m != nil {
		avgRTT, _ = strconv.ParseFloat(m[2], 64)
		hasRTT = true
	}

	if lossPct == -1 && !hasRTT {
		return 0, 0, false, nil
	}
	return lossPct, avgRTT, true, nil
}

var lteSignalRe = regexp.MustCompile(`mLte=CellSignalStrengthLte:[^,]*rsrp=(-?\d+)`)
var rssnrRe = regexp.MustCompile(`rssnr=(-?\d+)`)
var dataRegRe = regexp.MustCompile(`mDataRegState=(\d+)`)
var manualSelRe = regexp.MustCompile(`isManualNetworkSelection=(true|false)`)

func telephonyDump() (string, error) {
	out, err := exec.Command("dumpsys", "telephony.registry").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("dumpsys telephony.registry: %w", err)
	}
	return string(out), nil
}

func (RealController) SignalReading() (int, int, bool, error) {
	text, err := telephonyDump()
	if err != nil {
		return 0, 0, false, err
	}
	m := lteSignalRe.FindStringSubmatch(text)
	if m == nil {
		return 0, 0, false, nil
	}
	rsrp, _ := strconv.Atoi(m[1])
	sinr := 0
	if sm := rssnrRe.FindStringSubmatch(text); sm != nil {
		sinr, _ = strconv.Atoi(sm[1])
	}
	return rsrp, sinr, true, nil
}

func (RealController) DataRegistrationState() (int, bool, error) {
	text, err := telephonyDump()
	if err != nil {
		return 0, false, err
	}
	m := dataRegRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false, nil
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

func (RealController) IsManualNetworkSelection() (bool, error) {
	text, err := telephonyDump()
	if err != nil {
		return false, err
	}
	m := manualSelRe.FindStringSubmatch(text)
	return m != nil && m[1] == "true", nil
}

// ReconnectCellular mirrors V1's guarded airplane-mode toggle -
// restoring airplane mode to off is deferred so it always runs even
// if this function returns early on an error, the same class of fix
// V1 needed after a real incident where an unguarded version left
// airplane mode stuck on.
func (RealController) ReconnectCellular() error {
	restoreOff := func() { exec.Command("settings", "put", "global", "airplane_mode_on", "0").Run() }
	defer restoreOff()

	if err := run("settings", "put", "global", "airplane_mode_on", "1"); err != nil {
		return fmt.Errorf("enable airplane mode: %w", err)
	}
	time.Sleep(3 * time.Second)
	if err := run("settings", "put", "global", "airplane_mode_on", "0"); err != nil {
		return fmt.Errorf("disable airplane mode: %w", err)
	}
	return nil
}

// --- Settings drift ---

func (RealController) AssertConntrackMax() (bool, error) {
	data, err := os.ReadFile("/proc/sys/net/netfilter/nf_conntrack_max")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(data)) == "65536" {
		return false, nil
	}
	return true, run("sysctl", "-w", "net.netfilter.nf_conntrack_max=65536")
}

func (RealController) AssertPrivateDNS() (bool, error) {
	mode, _ := settingsGet("global", "private_dns_mode")
	spec, _ := settingsGet("global", "private_dns_specifier")
	if mode == "hostname" && spec == "1dot1dot1dot1.cloudflare-dns.com" {
		return false, nil
	}
	if err := run("settings", "put", "global", "private_dns_mode", "hostname"); err != nil {
		return false, err
	}
	return true, run("settings", "put", "global", "private_dns_specifier", "1dot1dot1dot1.cloudflare-dns.com")
}

func (RealController) AssertSoftApTimeoutDisabled() (bool, error) {
	v, err := settingsGet("global", "soft_ap_timeout_enabled")
	if err != nil || v == "0" {
		return false, err
	}
	return true, run("settings", "put", "global", "soft_ap_timeout_enabled", "0")
}

func (RealController) AssertPreferredNetworkMode() (bool, error) {
	v, err := settingsGet("global", "preferred_network_mode")
	if err != nil {
		return false, err
	}
	v = strings.SplitN(v, ",", 2)[0]
	if v == expectedNetworkMode {
		return false, nil
	}
	return true, run("settings", "put", "global", "preferred_network_mode", expectedNetworkMode)
}
