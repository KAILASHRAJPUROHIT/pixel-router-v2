package failover

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Controller is everything the failover decision logic needs from the
// outside world - mirrors V1's failover_monitor.sh function-by-
// function so the port is easy to audit against the original.
type Controller interface {
	CellularReachable() bool
	// MeasureCellularMbps mirrors measure_cellular_mbps: a cheap
	// passive byte-counter sample first, falling back to a real ~1MB
	// active download only if the passive reading looks low (V1's own
	// 2026-07-10 hardening - passive alone false-triggered on an idle
	// but healthy link).
	MeasureCellularMbps() float64
	AirplaneModeOn() (bool, error)
	DisableAirplaneMode() error

	WifiPresence(mac string) bool
	BTPresence(btMac string) bool
	SendSMS(number, message string) error

	// BorrowedConnectionAlive mirrors borrowed_connection_alive: wifi
	// must be on AND actively connected to one of the two known SSIDs.
	// Returns the actual live SSID (which may differ from what we last
	// recorded, if Android silently roamed between the two saved
	// networks) and whether it's alive at all.
	BorrowedConnectionAlive(ssid1, ssid2 string) (liveSSID string, alive bool)

	// ScanAndConnect does a single scan+connect attempt against
	// whichever of the two SSIDs is currently visible, returning the
	// SSID it connected to (or "" if neither was found).
	ScanAndConnect(p1, p2 Phone) string
	ForgetNetwork(ssid string)
	InternetReachableViaBorrowed() bool
}

const (
	lowSpeedThresholdMbps = 1.0
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

func settingsGet(namespace, key string) (string, error) {
	out, err := exec.Command("settings", "get", namespace, key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (RealController) CellularReachable() bool {
	return exec.Command("ping", "-c", "1", "-W", "2", "8.8.8.8").Run() == nil
}

var rmnetDevLineRe = regexp.MustCompile(`^\s*(rmnet\d+):\s*(.*)$`)

func detectWanIf() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) && strings.HasPrefix(fields[i+1], "rmnet") {
				return fields[i+1]
			}
		}
	}
	// Fall back to whichever rmnet* interface has moved the most bytes.
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return ""
	}
	best, bestTotal := "", int64(-1)
	for _, line := range strings.Split(string(data), "\n") {
		m := rmnetDevLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fields := strings.Fields(m[2])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		if rx+tx > bestTotal {
			best, bestTotal = m[1], rx+tx
		}
	}
	return best
}

func rxBytes(iface string) int64 {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		m := rmnetDevLineRe.FindStringSubmatch(line)
		if m == nil || m[1] != iface {
			continue
		}
		fields := strings.Fields(m[2])
		if len(fields) < 1 {
			return 0
		}
		v, _ := strconv.ParseInt(fields[0], 10, 64)
		return v
	}
	return 0
}

func (RealController) MeasureCellularMbps() float64 {
	wan := detectWanIf()
	if wan == "" {
		return 0
	}
	r1 := rxBytes(wan)
	time.Sleep(5 * time.Second)
	r2 := rxBytes(wan)
	deltaBytes := r2 - r1
	if deltaBytes < 0 {
		deltaBytes = 0
	}
	passive := float64(deltaBytes) / 5 * 8 / 1_000_000
	if passive >= lowSpeedThresholdMbps {
		return passive
	}
	return measureActiveDownloadMbps()
}

// measureActiveDownloadMbps mirrors V1's real-download probe: passive
// byte-counting alone reads near-zero on an idle-but-healthy link, so
// a low passive reading only gets treated as a real problem after
// confirming it with an actual transfer. Uses the same two known-good
// plain-HTTP test URLs V1 settled on after their first choice
// (cachefly) silently started returning a tiny placeholder response
// instead of real bytes.
func measureActiveDownloadMbps() float64 {
	testURLs := []string{
		"http://speedtest.tele2.net/1MB.zip",
		"http://ipv4.download.thinkbroadband.com/1MB.zip",
	}
	bestSize := int64(0)
	bestElapsed := time.Millisecond
	for _, url := range testURLs {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		start := time.Now()
		size := int64(0)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			client := &http.Client{Timeout: 8 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				n, _ := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				size = n
			}
		}
		cancel()
		elapsed := time.Since(start)
		if elapsed <= 0 {
			elapsed = time.Millisecond
		}
		if size > bestSize {
			bestSize, bestElapsed = size, elapsed
		}
		if size >= 10000 {
			break
		}
	}
	if bestSize == 0 {
		// Indeterminate (ping worked but every download failed/returned
		// nothing) - same as V1, don't let this alone drive a failover.
		return lowSpeedThresholdMbps
	}
	return float64(bestSize) * 8 / 1_000_000 / bestElapsed.Seconds()
}

func (RealController) AirplaneModeOn() (bool, error) {
	v, err := settingsGet("global", "airplane_mode_on")
	return v == "1", err
}

func (RealController) DisableAirplaneMode() error {
	return run("settings", "put", "global", "airplane_mode_on", "0")
}

func detectApIf() string {
	if exec.Command("ip", "addr", "show", "ap_br_wlan2").Run() == nil {
		return "ap_br_wlan2"
	}
	return "wlan2"
}

func (RealController) WifiPresence(mac string) bool {
	if mac == "" {
		return false
	}
	out, err := exec.Command("ip", "neigh", "show", "dev", detectApIf()).Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(mac))
}

func (RealController) BTPresence(btMac string) bool {
	if btMac == "" {
		return false
	}
	v, err := settingsGet("global", "bluetooth_on")
	if err != nil || v != "1" {
		return false
	}
	runIgnoreErr("am", "start-service", "-n", "com.router.aod/.BtScanService")
	time.Sleep(16 * time.Second)
	data, err := os.ReadFile("/data/data/com.router.aod/files/bt_scan_result.txt")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), strings.ToLower(btMac))
}

func (RealController) SendSMS(number, message string) error {
	if number == "" {
		return nil
	}
	return run("am", "broadcast", "-n", "com.router.aod/.SendSmsReceiver",
		"-a", "com.router.aod.SEND_SMS", "--es", "number", number, "--es", "message", message)
}

var wifiConnectedToRe = regexp.MustCompile(`Wifi is connected to "([^"]*)"`)

func (RealController) BorrowedConnectionAlive(ssid1, ssid2 string) (string, bool) {
	v, err := settingsGet("global", "wifi_on")
	if err != nil || v != "1" {
		return "", false
	}
	out, err := exec.Command("cmd", "wifi", "status").Output()
	if err != nil {
		return "", false
	}
	m := wifiConnectedToRe.FindStringSubmatch(string(out))
	if m == nil {
		return "", false
	}
	live := m[1]
	if live == ssid1 || live == ssid2 {
		return live, true
	}
	return "", false
}

func networkIDForSSID(ssid string) (string, bool) {
	out, err := exec.Command("cmd", "wifi", "list-networks").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, ssid) {
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] != "Network" {
				return fields[0], true
			}
		}
	}
	return "", false
}

func (RealController) ForgetNetwork(ssid string) {
	if ssid == "" {
		return
	}
	if id, ok := networkIDForSSID(ssid); ok {
		runIgnoreErr("cmd", "wifi", "forget-network", id)
	}
}

func (RealController) ScanAndConnect(p1, p2 Phone) string {
	runIgnoreErr("cmd", "wifi", "start-scan")
	time.Sleep(8 * time.Second)
	out, _ := exec.Command("cmd", "wifi", "list-scan-results").Output()
	results := string(out)
	if p1.SSID != "" && strings.Contains(results, p1.SSID) {
		runIgnoreErr("cmd", "wifi", "connect-network", p1.SSID, "wpa2", p1.Password)
		return p1.SSID
	}
	if p2.SSID != "" && strings.Contains(results, p2.SSID) {
		runIgnoreErr("cmd", "wifi", "connect-network", p2.SSID, "wpa2", p2.Password)
		return p2.SSID
	}
	return ""
}

func (RealController) InternetReachableViaBorrowed() bool {
	time.Sleep(10 * time.Second) // let tethering re-home its upstream first
	return exec.Command("ping", "-c", "1", "-W", "3", "8.8.8.8").Run() == nil
}
