package aodstats

import (
	"sync"
	"time"
)

// BoolCache is RateCache's sibling for any (bool, error) health check
// that's too slow to call inline per request. The concrete case found
// live 2026-07-11: forwarder health relies on `pm dump <package>`,
// which alone measured 6.35s on this device - called synchronously
// inside every /internal/aod-stats request, that one command was
// responsible for nearly all of the "AOD struggling" slowness. A
// forwarder health check doesn't need to be fresher than a few tens
// of seconds (the standalone forwarder service itself only re-checks
// every 20s), so refreshing in the background and having the request
// handler read the last result is both correct and instant.
type BoolCache struct {
	mu         sync.RWMutex
	val        bool
	err        error
	hasReading bool
}

// NewBoolCache starts the background refresher and returns a cache
// ready to be read via Get. checkFn is called once per refreshInterval.
func NewBoolCache(refreshInterval time.Duration, checkFn func() (bool, error)) *BoolCache {
	bc := &BoolCache{}
	go bc.run(refreshInterval, checkFn)
	return bc
}

func (bc *BoolCache) run(refreshInterval time.Duration, checkFn func() (bool, error)) {
	for {
		val, err := checkFn()
		bc.mu.Lock()
		bc.val, bc.err, bc.hasReading = val, err, true
		bc.mu.Unlock()
		time.Sleep(refreshInterval)
	}
}

// Get returns the most recently checked value. Matches the
// func() (bool, error) shape Sources.ForwarderHealthy expects; before
// the first check completes it reports an error rather than a
// possibly-wrong default of true/false.
func (bc *BoolCache) Get() (bool, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	if !bc.hasReading {
		return false, errNoReadingYet
	}
	return bc.val, bc.err
}
