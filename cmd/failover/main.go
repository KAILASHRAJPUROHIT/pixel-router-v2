// Command failover ports V1's failover_monitor.sh - the borrowed-
// hotspot escalation cascade (Bluetooth presence -> WiFi presence ->
// SMS-triggered iPhone Shortcut) that keeps our own PILLYBOO hotspot's
// clients online when the router's own cellular connection goes bad.
// The last major V1 feature not yet ported to V2.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/failover"
)

func main() {
	configPath := flag.String("config", "/data/adb/dashboard/failover.properties", "path to the phone/SSID/SMS config - read-only, same file V1 already uses")
	stateDir := flag.String("state-dir", "/data/adb/router_v2/state", "directory for this service's own small state files")
	checkInterval := flag.Duration("check-interval", 120*time.Second, "how often to check cellular health while on our own connection")
	flag.Parse()

	cfg, err := failover.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	paths := failover.FilePaths{
		InternetType: *stateDir + "/failover_internet_type.txt",
		Prompt:       *stateDir + "/failover_prompt.txt",
		BorrowedSSID: *stateDir + "/failover_borrowed_ssid.txt",
	}

	failover.Run(failover.RealController{}, cfg, paths, *checkInterval)
}
