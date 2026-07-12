package aodstats

import (
	"errors"
	"sync"
	"time"
)

var errNoReadingYet = errors.New("no rate reading yet")

// RateCache keeps the last-computed cellular down/up rate, refreshed
// on its own background timer instead of inline per HTTP request.
// RealDownUpMbps needs to sleep across a sample window to compute a
// rate at all - doing that synchronously inside the aod-stats request
// handler meant every single poll paid that sleep as fixed latency,
// which got a lot worse under thermal throttling (confirmed live
// 2026-07-11: requests were taking 6+ seconds, reading to the AOD app
// as "struggling"). Handlers now just read the last cached value,
// which is instant.
type RateCache struct {
	mu         sync.RWMutex
	down, up   float64
	hasReading bool
}

// NewRateCache starts the background sampler and returns a cache ready
// to be read via Get. sampleWindow is passed straight through to
// RealDownUpMbps for each refresh.
func NewRateCache(sampleWindow time.Duration) *RateCache {
	rc := &RateCache{}
	go rc.run(sampleWindow)
	return rc
}

func (rc *RateCache) run(sampleWindow time.Duration) {
	for {
		down, up, err := RealDownUpMbps(sampleWindow)
		if err == nil {
			rc.mu.Lock()
			rc.down, rc.up, rc.hasReading = down, up, true
			rc.mu.Unlock()
		} else {
			// RealDownUpMbps can return before sleeping through
			// sampleWindow (its first /proc/net/dev read failed) -
			// without this, a persistent read failure would spin the
			// loop as fast as the CPU allows.
			time.Sleep(sampleWindow)
		}
	}
}

// Get returns the most recently sampled rate. Matches the
// func() (down, up float64, err error) shape Sources.DownUpMbps
// expects; err is non-nil only until the first sample completes.
func (rc *RateCache) Get() (down, up float64, err error) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if !rc.hasReading {
		return 0, 0, errNoReadingYet
	}
	return rc.down, rc.up, nil
}
