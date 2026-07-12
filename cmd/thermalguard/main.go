// Command thermalguard ports V1's thermal_monitor.sh - the CPU/GPU
// frequency safety net plus charge-current clamp. Ported before any
// V1 retirement since it's the one safety-critical piece with no
// prior V2 coverage.
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/antriva/pixel-router-v2/internal/thermalguard"
)

func main() {
	interval := flag.Duration("check-interval", 30*time.Second, "how often to check thermal/charge state")
	throttledStateFile := flag.String("throttled-state-file", "/data/adb/router_v2/state/thermal_throttled", "flat file mirroring current-throttled state, for other processes (powergovernor) to read cross-process")
	flag.Parse()

	ctrl := thermalguard.RealController{}
	st := &thermalguard.State{}
	// Written unconditionally at startup (not just on later transitions)
	// so a freshly-started powergovernor process never reads a stale or
	// missing file before the first real thermal event.
	os.WriteFile(*throttledStateFile, []byte("0"), 0644)

	for {
		result := thermalguard.Tick(ctrl, st)
		if result.ThrottleChanged {
			if result.NowThrottled {
				log.Println("thermal status SEVERE+ - CPU/GPU frequency capped, modem/WiFi untouched")
				os.WriteFile(*throttledStateFile, []byte("1"), 0644)
			} else {
				log.Println("thermal status back to normal - CPU/GPU frequency restored to baseline")
				os.WriteFile(*throttledStateFile, []byte("0"), 0644)
			}
		}
		if result.BaselineReapplied {
			log.Println("baseline frequency had drifted with no thermal event to explain it - reapplied")
		}
		if result.ChargeLimitChanged {
			if result.NowChargeLimited {
				log.Println("skin temp and battery both high - charge current clamped")
			} else {
				log.Println("cooled below threshold - charge current restored")
			}
		}
		time.Sleep(*interval)
	}
}
