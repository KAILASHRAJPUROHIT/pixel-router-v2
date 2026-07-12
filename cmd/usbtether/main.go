// The usbtether service owns USB NCM tethering bring-up and recovery
// exclusively - V1 split this across a boot script and a separate
// watchdog; consolidating into one owner is the same reasoning as
// internal/hotspot.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/usbtether"
)

func main() {
	dexPath := flag.String("dex", "/data/adb/router_v2/assets/tether-starter.dex", "path to the compiled TetherStarter.dex")
	workDir := flag.String("workdir", "/data/local/tmp/router_v2_tether", "app_process working directory")
	checkInterval := flag.Duration("check-interval", 30*time.Second, "how often to verify ncm0 is up")
	maxAttempts := flag.Int("max-attempts", 4, "bring-up retries before giving up for this cycle")
	settleDelay := flag.Duration("settle-delay", 3*time.Second, "delay after each attempt before rechecking")
	flag.Parse()

	starter := usbtether.RealStarter{DexPath: *dexPath, WorkDir: *workDir}

	bringUp := func(reason string) {
		if usbtether.IsUp() {
			return
		}
		log.Printf("ncm0 down (%s) - attempting bring-up", reason)
		attempts, output, err := usbtether.EnsureUp(starter, *maxAttempts, *settleDelay)
		if err != nil {
			log.Printf("bring-up incomplete after %d attempts: %v (last output: %s)", attempts, err, output)
			return
		}
		log.Printf("ncm0 up after %d attempt(s)", attempts)
	}

	// Confirmed live 2026-07-12: ncm0 coming up (IsUp()==true) does NOT
	// guarantee Android's own tethering framework actually set up NAT/
	// FORWARD rules for it - a real post-reboot case left the interface
	// fully up with zero route to the internet. Asserted independently
	// of bring-up, every cycle, since the WAN interface can also change
	// (SIM swap, RAT change) after ncm0 was already fine.
	assertNAT := func() {
		if !usbtether.IsUp() {
			return
		}
		wan := usbtether.DetectWanInterface()
		if changed, err := usbtether.AssertNATRules(wan); err != nil {
			log.Printf("NAT assertion failed for ncm0<->%s: %v", wan, err)
		} else if changed {
			log.Printf("NAT/FORWARD rules for ncm0<->%s were missing - added (Android's own tethering framework hadn't set them up)", wan)
		}
	}

	bringUp("startup")
	assertNAT()
	for {
		time.Sleep(*checkInterval)
		bringUp("periodic check")
		assertNAT()
	}
}
