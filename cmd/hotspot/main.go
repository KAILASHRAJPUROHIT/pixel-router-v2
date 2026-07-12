// The hotspot service owns hotspot bring-up and recovery exclusively -
// in V1 this same responsibility was split across a boot script plus
// three independent watchdogs, all of which could call the bring-up
// sequence concurrently, requiring a file-based mutex to avoid a race
// that had actually caused an incomplete single-band-only result live.
// One owning service structurally can't race itself.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/antriva/pixel-router-v2/internal/hotspot"
)

// fileConfig's SSID is now split by which band-mode it names, since a
// single physical hotspot can only ever broadcast one SSID at a time on
// this hardware - Android's bridged dual-band mode shares ONE SSID
// across both radios (confirmed live 2026-07-12: no independent
// dual-AP capability flag present, and the underlying hostapd process
// runs with zero argv, configured entirely via Android's HAL rather
// than reading its per-interface conf files directly, so hand-editing
// those to diverge the SSID per radio isn't safely achievable either).
// So instead of two simultaneous names, Band2GHzEnabled toggles between
// two full states: 5GHz-only day-to-day (lower interference, all
// devices in one room support it) and full bridged dual-band on demand
// (shared SSID, needed only for older/peripheral devices that are
// 2.4GHz-only).
type fileConfig struct {
	SSID5GhzOnly    string   `json:"ssid_5ghz_only"`
	SSIDDualBand    string   `json:"ssid_dual_band"`
	Password        string   `json:"password"`
	Band2GHzEnabled bool     `json:"band_2ghz_enabled"`
	Deny2GhzMACs    []string `json:"deny_2ghz_macs"`
}

func loadConfig(path string) (hotspot.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hotspot.Config{}, err
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return hotspot.Config{}, err
	}
	ssid, band := fc.SSID5GhzOnly, "5"
	if fc.Band2GHzEnabled {
		ssid, band = fc.SSIDDualBand, "bridged_2_5"
	}
	return hotspot.Config{
		SSID:         ssid,
		Password:     fc.Password,
		Band:         band,
		Deny2GhzMACs: fc.Deny2GhzMACs,
	}, nil
}

func main() {
	configPath := flag.String("config", "/data/adb/router_v2/hotspot_secrets.json", "path to hotspot config JSON")
	checkInterval := flag.Duration("check-interval", 15*time.Second, "how often to verify the AP is up")
	consecutiveDownNeeded := flag.Int("consecutive-down-needed", 3, "consecutive down readings required before restarting - avoids restarting (and deauthing every connected client) on a single transient blip")
	forceRestartPath := flag.String("force-restart-path", "/data/adb/router_v2/state/hotspot_force_restart", "trigger file the dashboard's 'force reconnect' button writes - checked every tick, deleted and acted on immediately if present")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v (refusing to start with a guessed value)", err)
	}

	log.Printf("bringing up hotspot SSID=%s band=%s", cfg.SSID, cfg.Band)
	if err := hotspot.BringUp(cfg); err != nil {
		log.Fatalf("initial bring-up failed: %v", err)
	}
	log.Println("hotspot up")

	// Confirmed live 2026-07-12: a bring-up cycle tears the AP radio
	// down and back up (cmd wifi stop-softap then start-softap), which
	// deauthorizes every currently-connected client as a side effect -
	// caught in dmesg as a burst of WLC_SCB_DEAUTHORIZE events across
	// every connected MAC. Restarting on a single "down" reading meant
	// a brief, transient blip (e.g. a DFS channel-availability-check
	// delay on 5GHz, which can take up to 60s, or the bridge interface
	// briefly lacking an IPv4 address while it settles) could trigger
	// an unnecessary full restart - which itself can produce another
	// transient "down" reading during its own settling window,
	// compounding into repeated restarts just minutes (or, seen live,
	// under 90s) apart. Requiring several consecutive down readings
	// before restarting - the same debounce pattern already used for
	// quota/thermal/failover elsewhere in this codebase - means a real,
	// sustained failure still gets caught and fixed, just not a blip.
	downCount := 0
	for {
		time.Sleep(*checkInterval)

		if _, err := os.Stat(*forceRestartPath); err == nil {
			os.Remove(*forceRestartPath)
			log.Println("force-restart requested via dashboard - restarting now")
			if err := hotspot.BringUp(cfg); err != nil {
				log.Printf("forced restart failed: %v", err)
			} else {
				log.Println("hotspot force-restarted successfully")
			}
			downCount = 0
			continue
		}

		if hotspot.IsUp() {
			downCount = 0
			continue
		}
		downCount++
		if downCount < *consecutiveDownNeeded {
			log.Printf("AP interface down - %d/%d consecutive checks before restarting", downCount, *consecutiveDownNeeded)
			continue
		}
		log.Println("AP interface down - restarting")
		if err := hotspot.BringUp(cfg); err != nil {
			log.Printf("restart failed: %v - will retry next check", err)
			continue
		}
		log.Println("hotspot restarted successfully")
		downCount = 0
	}
}
