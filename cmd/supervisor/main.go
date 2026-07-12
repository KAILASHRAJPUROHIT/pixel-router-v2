// V2's supervisor daemon - the direct replacement for V1's
// service_supervisor.sh + N independent watchdog scripts. Reads a
// service manifest, starts each service, and restarts on actual death
// (via Wait(), not a poll interval) with crash-loop backoff.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/antriva/pixel-router-v2/internal/buildinfo"
	"github.com/antriva/pixel-router-v2/internal/pidlock"
	"github.com/antriva/pixel-router-v2/internal/supervisor"
)

// sendSMS reuses the exact same explicit-component broadcast mechanism
// internal/failover's RealController already uses - Android 8+ blocks
// implicit broadcasts to manifest-declared receivers, so this must
// name the receiver directly.
func sendSMS(number, message string) error {
	if number == "" {
		return nil
	}
	return exec.Command("am", "broadcast", "-n", "com.router.aod/.SendSmsReceiver",
		"-a", "com.router.aod.SEND_SMS", "--es", "number", number, "--es", "message", message).Run()
}

type Manifest struct {
	Services []supervisor.ServiceSpec `json:"services"`
}

func main() {
	manifestPath := flag.String("manifest", "/data/adb/router_v2/services.json", "path to service manifest JSON")
	statusPath := flag.String("status", "/data/adb/router_v2/state/supervisor_status.json", "path to write live status JSON")
	logPath := flag.String("log", "/data/adb/router_v2/logs/supervisor.jsonl", "path to structured supervisor log")
	serviceLogDir := flag.String("service-log-dir", "/data/adb/router_v2/logs", "directory to capture each service's own stdout/stderr into <name>.log")
	pidfilePath := flag.String("pidfile", "/data/adb/router_v2/state/supervisor.pid", "single-instance lock file - a second supervisor launched while this one holds it fails immediately instead of racing for shared ports/chains")
	alertNumber := flag.String("alert-number", "", "phone number to SMS when any service crash-loops (empty disables alerting)")
	alertThreshold := flag.Int("alert-threshold", 3, "restarts within alert-window to trigger a crash-loop SMS")
	alertWindow := flag.Duration("alert-window", 5*time.Minute, "sliding window for counting restarts toward alert-threshold")
	alertCooldown := flag.Duration("alert-cooldown", 30*time.Minute, "minimum time between repeat alerts for the same service")
	flag.Parse()

	log.Printf("supervisor starting, version %s", buildinfo.Version)

	lockFile, err := pidlock.Acquire(*pidfilePath)
	if err != nil {
		log.Fatalf("refusing to start: %v", err)
	}
	if lockFile != nil {
		defer lockFile.Close()
	}

	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		log.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		log.Fatalf("parse manifest: %v", err)
	}
	if len(manifest.Services) == 0 {
		log.Fatalf("manifest has no services defined")
	}

	sup, err := supervisor.New(*statusPath, *logPath, *serviceLogDir)
	if err != nil {
		log.Fatalf("init supervisor: %v", err)
	}
	if *alertNumber != "" {
		sup.Alerter = supervisor.NewCrashLoopAlerter(*alertThreshold, *alertWindow, *alertCooldown, func(msg string) error {
			return sendSMS(*alertNumber, msg)
		})
	}

	for _, spec := range manifest.Services {
		spec := spec
		go sup.Run(spec)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	log.Println("shutdown signal received, stopping all services")
	sup.StopAll()
	// Give children a moment to be observed exiting/backing off before
	// this process itself exits.
	time.Sleep(2 * time.Second)
}
