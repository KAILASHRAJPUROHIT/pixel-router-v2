// Command powergovernor ports V1's aod_power_governor.sh.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/powergovernor"
)

func main() {
	interval := flag.Duration("check-interval", 2*time.Second, "how often to re-check dream/wakefulness state - short specifically so the snap-back on wake is fast enough not to be noticeable")
	flag.Parse()

	ctrl := powergovernor.RealController{}
	st := &powergovernor.State{LastTier: powergovernor.TierBase}

	for {
		result := powergovernor.Tick(ctrl, st)
		if result.TierChanged {
			log.Printf("%s (%s) - clocks set to %s tier", result.Source, result.NewTier, result.NewTier)
		}
		time.Sleep(*interval)
	}
}
