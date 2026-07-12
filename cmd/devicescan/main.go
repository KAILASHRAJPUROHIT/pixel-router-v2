// Command devicescan is the missing scanner half of V2's per-device
// tracking (see internal/devicescan) - periodically records every
// currently-connected client MAC into the SQLite devices table that
// /internal/aod-stats and /api/devices both read from.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/devicescan"
	"github.com/antriva/pixel-router-v2/internal/store"
)

func main() {
	dbPath := flag.String("db", "/data/adb/router_v2/state/router.db", "path to SQLite state db")
	interval := flag.Duration("check-interval", 30*time.Second, "how often to scan for connected clients")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctrl := devicescan.NewRealController()

	tickCount := 0
	const heartbeatEvery = 20 // ~every 10 min at the default 30s interval
	for {
		seen, pruned, err := devicescan.Tick(ctrl, st)
		if err != nil {
			log.Printf("tick error: %v", err)
		}
		if pruned > 0 {
			log.Printf("pruned %d stale device(s)", pruned)
		}
		tickCount++
		if tickCount%heartbeatEvery == 0 {
			log.Printf("heartbeat - %d live client(s) seen this tick", seen)
		}
		time.Sleep(*interval)
	}
}
