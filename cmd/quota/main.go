package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/quota"
	"github.com/antriva/pixel-router-v2/internal/store"
)

func main() {
	dbPath := flag.String("db", "/data/adb/router_v2/state/router.db", "path to SQLite state db")
	cutoffGB := flag.Float64("cutoff-gb", 300, "monthly cellular data cutoff in GB")
	notifyNumber := flag.String("notify-number", "", "owner phone number to SMS on cutoff (empty disables notification)")
	checkInterval := flag.Duration("check-interval", 5*time.Minute, "how often to check usage")
	billingDay := flag.Int("billing-day", 0, "day of the month the billing cycle resets (1-28); 0 leaves whatever is already stored unchanged")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if *billingDay != 0 {
		if err := st.SetBillingDay(*billingDay); err != nil {
			log.Fatalf("set billing day: %v", err)
		}
		log.Printf("billing cycle reset day set to %d", *billingDay)
	}

	ctrl := quota.RealController{NotifyNumber: *notifyNumber}
	cfg := quota.Config{CutoffGB: *cutoffGB}

	for {
		if err := quota.Tick(ctrl, st, cfg, time.Now()); err != nil {
			log.Printf("tick error: %v", err)
		}
		time.Sleep(*checkInterval)
	}
}
