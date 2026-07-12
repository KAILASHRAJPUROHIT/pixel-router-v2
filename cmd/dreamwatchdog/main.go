// Command dreamwatchdog ports V1's aod_watchdog.sh (settings/component
// drift correction, ~120s) and aod_dream_recovery.sh (fast dream-
// takeover recovery, 5-60s adaptive) into one process - the one V1 AOD
// management responsibility not yet covered anywhere in V2.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/dreamwatchdog"
)

func main() {
	settingsInterval := flag.Duration("settings-check-interval", 120*time.Second, "how often to re-assert settings/component drift")
	flag.Parse()

	ctrl := dreamwatchdog.RealController{}

	go func() {
		for {
			changed := dreamwatchdog.TickSettingsDrift(ctrl)
			if len(changed) > 0 {
				log.Printf("settings drift corrected: %v", changed)
			}
			time.Sleep(*settingsInterval)
		}
	}()

	st := dreamwatchdog.NewRecoveryState()
	wasPaused := false
	wasNotCharging := false
	tickCount := 0
	const heartbeatEvery = 60 // ~ every few minutes at typical tick intervals

	for {
		result := dreamwatchdog.TickDreamRecovery(ctrl, st, time.Now())
		tickCount++

		if result.ThermalPause != wasPaused {
			if result.ThermalPause {
				log.Printf("thermal pause engaged - not fighting for the dream until temp cools")
			} else {
				log.Printf("thermal pause released - resuming normal dream-recovery checks")
			}
			wasPaused = result.ThermalPause
		}
		if result.NotCharging != wasNotCharging {
			if result.NotCharging {
				log.Printf("unplugged - not fighting for the dream until back on charge (avoids fighting Android's own charge-tied dream policy)")
			} else {
				log.Printf("back on charge - resuming normal dream-recovery checks")
			}
			wasNotCharging = result.NotCharging
		}
		if result.Err != nil {
			log.Printf("dream recovery tick error: %v", result.Err)
		}
		if result.Recovered {
			log.Printf("dream wasn't ours - ran start-dreaming")
		}
		if result.DeepCheck {
			log.Printf("%s", result.DeepCheckMsg)
		}
		// Heartbeat, matching V1's own script convention - previously
		// this process could sit completely silent for hours (either
		// genuinely healthy, or thermally paused) with no way to tell
		// "alive and idle" apart from "stuck" without live-debugging it.
		if tickCount%heartbeatEvery == 0 {
			log.Printf("heartbeat - %d ticks so far, thermal_pause=%v, next_interval=%s", tickCount, result.ThermalPause, result.NextInterval)
		}

		time.Sleep(result.NextInterval)
	}
}
