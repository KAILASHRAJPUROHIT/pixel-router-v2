package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/antriva/pixel-router-v2/internal/aodcontrol"
	"github.com/antriva/pixel-router-v2/internal/aodstats"
	"github.com/antriva/pixel-router-v2/internal/blocklist"
	"github.com/antriva/pixel-router-v2/internal/failover"
	"github.com/antriva/pixel-router-v2/internal/forwarder"
	"github.com/antriva/pixel-router-v2/internal/hotspot"
	"github.com/antriva/pixel-router-v2/internal/netcontrol"
	"github.com/antriva/pixel-router-v2/internal/smsviewer"
	"github.com/antriva/pixel-router-v2/internal/speedtest"
	"github.com/antriva/pixel-router-v2/internal/store"
	"github.com/antriva/pixel-router-v2/internal/usbtether"
	"github.com/antriva/pixel-router-v2/internal/webserver"
)

// readSecretFile trims exactly one trailing newline (what any editor
// or `echo` produces) without silently eating meaningful whitespace
// elsewhere in the value.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
}

// restartServiceByName looks up name's current pid in the supervisor's
// own status file and sends it SIGTERM via the external `kill` binary
// (rather than Go's syscall.Kill directly) so this file cross-compiles
// cleanly without a linux-only build tag - the supervisor's own
// restart:always policy relaunches the process once it exits, exactly
// like an organic crash-restart.
func restartServiceByName(statusPath, name string) error {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return err
	}
	var statuses map[string]struct {
		Pid int `json:"pid"`
	}
	if err := json.Unmarshal(data, &statuses); err != nil {
		return err
	}
	svc, ok := statuses[name]
	if !ok || svc.Pid == 0 {
		return fmt.Errorf("service %q not found or has no active pid", name)
	}
	return exec.Command("kill", "-TERM", fmt.Sprintf("%d", svc.Pid)).Run()
}

// loadOrCreatePCCommandKey reads the shared secret the PC-side poller
// presents to /pc-command.json, generating and persisting a fresh
// random one on first run - self-bootstrapping the same way this
// service's own session tokens are generated, so no manual setup step
// is needed before the PC watcher script can be configured with it.
func loadOrCreatePCCommandKey(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(key), 0600); err != nil {
		return "", err
	}
	return key, nil
}

func main() {
	// Statically-linked Go binaries on Android can't find a CA bundle at
	// any default Linux path (Android doesn't ship one there) - without
	// this, the speed test's TLS requests to speed.cloudflare.com fail
	// with "certificate signed by unknown authority" (same root cause
	// hit earlier with cloudflared/ngrok). Android's own system CA store
	// at this path is already in a per-file PEM format Go's loader can
	// read directly. Must be set before any TLS request in this process
	// - Go's x509 system cert pool is lazily built on first use and
	// caches the result, so this only works set this early.
	os.Setenv("SSL_CERT_DIR", "/system/etc/security/cacerts")

	// Loopback-only by default (hardened 2026-07-11): both the on-device
	// AOD app and cloudflared (the only intended consumers) already
	// connect via 127.0.0.1, so there was never a real reason to listen
	// on [::]/0.0.0.0 - doing so meant this port (and everything behind
	// requireSession, not just the localhost-gated routes) was reachable
	// from any device on the PILLYBOO hotspot's own LAN.
	addr := flag.String("addr", "127.0.0.1:9090", "listen address")
	dbPath := flag.String("db", "/data/adb/router_v2/state/router.db", "path to SQLite state db")
	username := flag.String("username", "Antriva", "dashboard username")
	passwordHashFile := flag.String("password-hash-file", "", "path to a file containing the bcrypt password hash - not passed as a bare flag value, which would be visible to any other root process via ps")
	totpSecretFile := flag.String("totp-secret-file", "", "path to a file containing the base32 TOTP secret")
	supervisorStatusPath := flag.String("supervisor-status", "/data/adb/router_v2/state/supervisor_status.json", "path to the supervisor's live status JSON")
	forwarderPackage := flag.String("forwarder-package", "cn.ppps.forwarder", "SMS/call forwarder app package name")
	hotspotForceRestartPath := flag.String("hotspot-force-restart-path", "/data/adb/router_v2/state/hotspot_force_restart", "trigger file the hotspot service polls for to force an immediate restart")
	failoverInternetTypePath := flag.String("failover-internet-type-path", "/data/adb/router_v2/state/failover_internet_type.txt", "path failover/vpntether write the current uplink type to (self/borrowed)")
	tunnelStatusPath := flag.String("tunnel-status-path", "/data/adb/router_v2/state/tunnel_status.txt", "path vpntether writes the WireGuard tunnel's alive/dead/not_present status to")
	pcCommandPath := flag.String("pc-command-path", "/data/adb/router_v2/state/pc_command.json", "where a queued PC remote-control command is written for the PC-side poller")
	pcCommandKeyFile := flag.String("pc-command-key-file", "/data/adb/router_v2/secrets/pc_command_key.txt", "shared secret file gating /pc-command.json - generated on first run if missing")
	speedtestCachePath := flag.String("speedtest-cache-path", "/data/adb/router_v2/state/speedtest_result.json", "where the last on-demand speed test result is cached")
	customMessagePath := flag.String("custom-message-path", "/data/adb/router_v2/state/custom_message.txt", "where a timed custom AOD message is stored")
	hotspotConfigPath := flag.String("hotspot-config-path", "/data/adb/router_v2/secrets/hotspot.json", "path to the hotspot service's own config JSON - shared with cmd/hotspot")
	failoverConfigPath := flag.String("failover-config", "/data/adb/dashboard/failover.properties", "path to the phone/SSID config - read-only, same file the failover service reads")
	borrowedSSIDPath := flag.String("borrowed-ssid-path", "/data/adb/router_v2/state/failover_borrowed_ssid.txt", "path failover writes the currently-connected borrowed SSID to")
	flag.Parse()

	if *passwordHashFile == "" || *totpSecretFile == "" {
		log.Fatal("--password-hash-file and --totp-secret-file are required")
	}

	passwordHash, err := readSecretFile(*passwordHashFile)
	if err != nil {
		log.Fatalf("read password hash file: %v", err)
	}
	totpSecret, err := readSecretFile(*totpSecretFile)
	if err != nil {
		log.Fatalf("read totp secret file: %v", err)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Read-only, best-effort - if the config is missing/unparseable,
	// ResolveBorrowedName just falls back to the raw SSID or "Borrowed".
	failoverCfg, _ := failover.LoadConfig(*failoverConfigPath)

	pcCommandKey, err := loadOrCreatePCCommandKey(*pcCommandKeyFile)
	if err != nil {
		log.Fatalf("load/create pc command key: %v", err)
	}

	blockCtrl := blocklist.RealController{}
	// Re-assert every already-blocked device's iptables rule at
	// startup - the rule itself doesn't survive a full device reboot
	// (it's a live netfilter rule, not a persisted config file), so
	// without this a block would silently stop being enforced after
	// every reboot despite the dashboard still showing it as blocked.
	if devices, err := st.ListDevices(); err == nil {
		for _, d := range devices {
			if d.Blocked {
				if err := blockCtrl.Block(d.MAC); err != nil {
					log.Printf("failed to re-assert block for %s at startup: %v", d.MAC, err)
				}
			}
		}
	}

	fwdCtrl := forwarder.RealController{Package: *forwarderPackage}
	netCtrl := netcontrol.RealController{}
	rateCache := aodstats.NewRateCache(1 * time.Second)
	// Deliberately read-only (IsEnabled/IsPermissionGranted only) - the
	// actual healing already happens continuously in the separate
	// `forwarder` service; this just reports status, it doesn't
	// duplicate the fix-it side effects into a status-read path.
	// Backgrounded because `pm dump` alone measured 6.35s on-device -
	// see internal/aodstats/boolcache.go.
	forwarderHealthCache := aodstats.NewBoolCache(20*time.Second, func() (bool, error) {
		enabled, err := fwdCtrl.IsEnabled()
		if err != nil || !enabled {
			return false, err
		}
		for _, perm := range []string{"RECEIVE_SMS", "READ_SMS", "SEND_SMS"} {
			granted, err := fwdCtrl.IsPermissionGranted(perm)
			if err != nil || !granted {
				return false, err
			}
		}
		return true, nil
	})

	srv := webserver.New(webserver.Config{
		Username:                 *username,
		PasswordHash:             passwordHash,
		TotpSecret:               totpSecret,
		HotspotUp:                hotspot.IsUp,
		USBTetherUp:              usbtether.IsUp,
		SupervisorStatusPath:     *supervisorStatusPath,
		HotspotForceRestartPath:  *hotspotForceRestartPath,
		FailoverInternetTypePath: *failoverInternetTypePath,
		TunnelStatusPath:         *tunnelStatusPath,
		BlockDevice:              blockCtrl.Block,
		UnblockDevice:            blockCtrl.Unblock,
		BorrowedSSIDPath:         *borrowedSSIDPath,
		Phone1Name:               failoverCfg.Phone1.Name,
		Phone1SSID:               failoverCfg.Phone1.SSID,
		Phone2Name:               failoverCfg.Phone2.Name,
		Phone2SSID:               failoverCfg.Phone2.SSID,
		RestartService: func(name string) error {
			return restartServiceByName(*supervisorStatusPath, name)
		},
		Reboot:        func() error { return exec.Command("reboot").Run() },
		FetchSMS:      smsviewer.Fetch,
		PCCommandPath: *pcCommandPath,
		PCCommandKey:  pcCommandKey,
		RunSpeedtest: func() (speedtest.Result, error) {
			return speedtest.Run(&http.Client{Timeout: 30 * time.Second})
		},
		SpeedtestCachePath: *speedtestCachePath,
		CustomMessagePath:  *customMessagePath,
		HotspotConfigPath:  *hotspotConfigPath,
		AodPrefs:           aodcontrol.RealController{},
		AodSources: aodstats.Sources{
			// Counts only devices seen within webserver.OnlineThreshold,
			// not the full historical/pruned-after-7-days list -
			// confirmed live 2026-07-12 that showing the historical
			// total here read as "7 devices connected right now" when
			// only 2 actually were, causing real (unfounded) security
			// alarm. Shared by both this dashboard's card and the
			// on-device AOD screen, which had the exact same problem.
			DeviceCount: func() (int, error) {
				devices, err := st.ListDevices()
				if err != nil {
					return 0, err
				}
				online := 0
				now := time.Now()
				for _, d := range devices {
					if now.Sub(d.LastSeen) < webserver.OnlineThreshold {
						online++
					}
				}
				return online, nil
			},
			CycleGB: func() (float64, error) {
				bc, err := st.GetBillingCycle()
				return float64(bc.CycleBytes) / 1_000_000_000, err
			},
			BatteryPercent:   aodstats.RealBatteryPercent,
			SkinTempC:        aodstats.RealSkinTempC,
			DownUpMbps:       rateCache.Get,
			ForwarderHealthy: forwarderHealthCache.Get,
			HotspotUp:        hotspot.IsUp,
			USBTetherUp:      usbtether.IsUp,
			RSRP: func() (int, bool, error) {
				info, err := netCtrl.GetSignalInfo()
				if err != nil {
					return 0, false, nil // no reading available yet, not a hard error
				}
				return info.RSRP, true, nil
			},
		},
	}, st)

	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
