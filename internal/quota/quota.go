// Package quota ports V1's cellular_quota_monitor.sh - a monthly
// cellular data cutoff (default 300GB) with a configurable billing-
// cycle reset day. Deliberately does not duplicate any borrowed-
// hotspot failover logic (a separate concern in V1, likely a separate
// package here later): once mobile data is disabled, cellular
// throughput simply reads as down to whatever failover logic exists,
// which already knows how to react to that on its own.
package quota

import (
	"fmt"
	"time"

	"github.com/antriva/pixel-router-v2/internal/store"
)

type Controller interface {
	CurrentRmnetBytes() (int64, error)
	DisableMobileData() error
	EnableMobileData() error
	// NotifyCutoff sends the same kind of owner-facing alert V1 sent
	// via SMS broadcast - implementations may no-op if no notification
	// channel is configured.
	NotifyCutoff(message string) error
}

type Config struct {
	CutoffGB float64
}

var DefaultConfig = Config{CutoffGB: 300}

// Tick runs one check cycle: billing-day rollover, byte accumulation
// (overflow/counter-reset safe, same as V1's awk delta calc - a raw
// counter drop means the interface reset, so the whole new reading is
// the delta, not a negative number), and cutoff enforcement. Meant to
// be called on a timer (V1 used 5 minutes) by the owning service.
func Tick(ctrl Controller, st *store.Store, cfg Config, now time.Time) error {
	bc, err := st.GetBillingCycle()
	if err != nil {
		return fmt.Errorf("get billing cycle: %w", err)
	}

	todayDay := now.Day()
	todayMonth := now.Format("2006-01")

	// Reset exactly once per billing period - guards against
	// re-resetting on every poll throughout the whole matching day.
	if todayDay == bc.BillingDay && bc.LastResetMonth != todayMonth {
		bc.CycleBytes = 0
		bc.LastResetMonth = todayMonth
		if bc.CutoffActive {
			if err := ctrl.EnableMobileData(); err != nil {
				return fmt.Errorf("re-enable mobile data on cycle reset: %w", err)
			}
			bc.CutoffActive = false
		}
	}

	curSum, err := ctrl.CurrentRmnetBytes()
	if err != nil {
		return fmt.Errorf("read rmnet bytes: %w", err)
	}

	var delta int64
	if curSum >= bc.PrevSum {
		delta = curSum - bc.PrevSum
	} else {
		// Counter reset (interface renumbered/reconnected) - the whole
		// new reading is the delta, not a negative number.
		delta = curSum
	}
	bc.CycleBytes += delta
	bc.PrevSum = curSum

	cycleGB := float64(bc.CycleBytes) / 1_000_000_000
	if !bc.CutoffActive && cycleGB >= cfg.CutoffGB {
		if err := ctrl.DisableMobileData(); err != nil {
			return fmt.Errorf("disable mobile data at cutoff: %w", err)
		}
		bc.CutoffActive = true
		msg := fmt.Sprintf("Router: cellular data hit the %.0fGB cutoff for this billing cycle. Mobile data disabled until day %d next month.", cfg.CutoffGB, bc.BillingDay)
		if err := ctrl.NotifyCutoff(msg); err != nil {
			// Notification failure shouldn't undo the cutoff itself -
			// log-and-continue is the caller's job; return the state
			// change as already applied.
			return fmt.Errorf("cutoff applied but notification failed: %w", err)
		}
	}

	return st.SaveBillingCycle(bc)
}
