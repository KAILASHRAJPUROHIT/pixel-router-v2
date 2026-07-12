package supervisor

import (
	"fmt"
	"sync"
	"time"
)

// CrashLoopAlerter tracks recent restart events per service and
// decides when to fire an SMS alert - previously only quota's own
// cutoff had an SMS notification path; any OTHER service crash-looping
// (webserver, ttlproxy, etc.) was silent unless someone happened to be
// reading supervisor_status.json. A sliding window (not the
// ServiceStatus.RestartCount field, which accumulates forever since
// supervisor start) means "3 restarts just now" and "3 restarts spread
// across the last two weeks" are correctly told apart, and a per-
// service cooldown stops a genuinely stuck service from paging every
// single restart forever.
type CrashLoopAlerter struct {
	Threshold int
	Window    time.Duration
	Cooldown  time.Duration
	// Notify is dependency-injected so the decision logic here is
	// testable without actually sending SMS - RealNotify (main.go) does
	// the real am broadcast.
	Notify func(message string) error

	mu        sync.Mutex
	history   map[string][]time.Time
	lastAlert map[string]time.Time
}

func NewCrashLoopAlerter(threshold int, window, cooldown time.Duration, notify func(string) error) *CrashLoopAlerter {
	return &CrashLoopAlerter{
		Threshold: threshold,
		Window:    window,
		Cooldown:  cooldown,
		Notify:    notify,
		history:   make(map[string][]time.Time),
		lastAlert: make(map[string]time.Time),
	}
}

// RecordRestart notes a restart of service at now and fires an alert
// (subject to cooldown) if the service has restarted Threshold+ times
// within Window. Safe to call with a nil *CrashLoopAlerter (no-op) so
// wiring it up is optional.
func (a *CrashLoopAlerter) RecordRestart(service string, now time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	fire, count := a.recordLocked(service, now)
	a.mu.Unlock()

	if fire && a.Notify != nil {
		msg := fmt.Sprintf("Router: %s has restarted %d times in the last %s - may need attention.", service, count, a.Window)
		a.Notify(msg)
	}
}

// recordLocked is the pure decision core, split out for direct unit
// testing without needing real goroutines/sleeps.
func (a *CrashLoopAlerter) recordLocked(service string, now time.Time) (fire bool, count int) {
	cutoff := now.Add(-a.Window)
	kept := a.history[service][:0]
	for _, t := range a.history[service] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	a.history[service] = kept
	count = len(kept)

	if count < a.Threshold {
		return false, count
	}
	if last, ok := a.lastAlert[service]; ok && now.Sub(last) < a.Cooldown {
		return false, count
	}
	a.lastAlert[service] = now
	return true, count
}
