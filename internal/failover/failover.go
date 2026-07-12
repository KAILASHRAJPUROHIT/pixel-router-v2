package failover

import (
	"log"
	"os"
	"strings"
	"time"
)

const (
	consecutiveBadNeeded  = 5 // ~10 min of sustained bad readings before failing over (at CheckInterval=120s)
	recoveryCheckInterval = 5 * time.Minute
	failoverPollInterval  = 30 * time.Second
	smsResendInterval     = 30 * time.Minute
	smsTriggerPhrase      = "FAILOVER_TRIGGER_PHRASE_DO_NOT_CHANGE"
	fastPollDuration      = 2 * time.Minute
	fastPollInterval      = 1 * time.Second
)

// FilePaths mirrors V1's flat-file state (STATE_FILE/PROMPT_FILE/
// borrowed_ssid) - kept as simple files rather than a new SQLite table
// since it's the same small amount of state V1 already used files for,
// and other V2 services (tunnel's url-file) already follow this
// pattern for exactly this kind of small persisted string.
type FilePaths struct {
	InternetType string // "self" or "borrowed"
	Prompt       string
	BorrowedSSID string
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeFile(path, content string) {
	os.WriteFile(path, []byte(content), 0644)
}

func removeFile(path string) {
	os.Remove(path)
}

// PresenceSMSDecision is the pure, testable core of the "fire an SMS
// the instant a phone is confirmed present, but only once per
// approach" logic from trigger_failover - reset the sent flag once
// presence is lost so a later re-approach (phone left and came back)
// re-triggers instead of staying silent forever.
func PresenceSMSDecision(present, alreadySentThisPresence bool) (shouldSend bool, nextSentFlag bool) {
	if !present {
		return false, false
	}
	if alreadySentThisPresence {
		return false, true
	}
	return true, true
}

// ShouldResendPeriodicSMS mirrors the catch-all periodic resend -
// true redundancy for a phone that's home with both WiFi and
// Bluetooth off (undetectable by either presence tier).
func ShouldResendPeriodicSMS(now, lastSent time.Time) bool {
	return lastSent.IsZero() || now.Sub(lastSent) >= smsResendInterval
}

// Run is the top-level entry point, equivalent to V1's single flat
// main loop that re-read state from a file every iteration and
// dispatched on it. Structured here as two mutually-handing-off
// functions instead (runSelf calls runBorrowed once an escalation
// succeeds, and vice versa) rather than a literal re-read-state-every-
// iteration loop - behaviorally equivalent, and avoids re-deriving
// "which mode am I in" from disk on every single pass when it's
// already known from the call site that just transitioned.
func Run(ctrl Controller, cfg Config, paths FilePaths, checkInterval time.Duration) {
	if _, err := os.Stat(paths.InternetType); os.IsNotExist(err) {
		writeFile(paths.InternetType, "self")
	}
	if readFile(paths.InternetType) == "borrowed" {
		runBorrowed(ctrl, cfg, paths)
	}
	runSelf(ctrl, cfg, paths, checkInterval)
}

// runSelf is the "on our own cellular connection" branch: airplane-
// mode guard, then reachability+speed check, accumulating bad readings
// until consecutiveBadNeeded is hit, at which point it runs the full
// failover escalation and, if that succeeds, hands off to runBorrowed
// (blocking) before resuming its own loop once that returns.
func runSelf(ctrl Controller, cfg Config, paths FilePaths, checkInterval time.Duration) {
	badCount := 0
	for {
		time.Sleep(checkInterval)

		if on, err := ctrl.AirplaneModeOn(); err == nil && on {
			ctrl.DisableAirplaneMode()
			log.Printf("airplane mode was ON - this alone explains any cellular-down readings, turned it off and skipping this check cycle")
			badCount = 0
			continue
		}

		if ctrl.CellularReachable() {
			mbps := ctrl.MeasureCellularMbps()
			if mbps < lowSpeedThresholdMbps {
				badCount++
				log.Printf("low throughput reading (%.2fMbps) - %d/%d", mbps, badCount, consecutiveBadNeeded)
			} else {
				if badCount > 0 {
					removeFile(paths.Prompt)
				}
				badCount = 0
			}
		} else {
			badCount++
			log.Printf("cellular unreachable - %d/%d", badCount, consecutiveBadNeeded)
		}

		if badCount >= consecutiveBadNeeded {
			runFailoverEscalation(ctrl, cfg, paths)
			badCount = 0
			if readFile(paths.InternetType) == "borrowed" {
				runBorrowed(ctrl, cfg, paths)
			}
		}
	}
}

// runBorrowed is the "currently on a borrowed hotspot" branch: confirm
// the borrowed WiFi connection is genuinely still alive (self-heals
// the recorded SSID if Android silently roamed between the two saved
// iPhone networks), then periodically re-check whether our own
// cellular has recovered. Returns once back to self (either the
// connection was lost, or our own cellular recovered), handing control
// back to runSelf's caller.
func runBorrowed(ctrl Controller, cfg Config, paths FilePaths) {
	for {
		liveSSID, alive := ctrl.BorrowedConnectionAlive(cfg.Phone1.SSID, cfg.Phone2.SSID)
		if !alive {
			log.Printf("borrowed hotspot connection lost - resetting to self so state doesn't show a false Borrowed status")
			writeFile(paths.InternetType, "self")
			removeFile(paths.BorrowedSSID)
			return
		}
		if recorded := readFile(paths.BorrowedSSID); recorded != liveSSID {
			log.Printf("borrowed_ssid was stale (recorded %q, actually connected to %q) - corrected", recorded, liveSSID)
			writeFile(paths.BorrowedSSID, liveSSID)
		}

		time.Sleep(recoveryCheckInterval)

		if ctrl.CellularReachable() {
			mbps := ctrl.MeasureCellularMbps()
			if mbps >= lowSpeedThresholdMbps {
				log.Printf("own cellular connection restored - disconnecting from borrowed hotspot")
				ctrl.ForgetNetwork(readFile(paths.BorrowedSSID))
				removeFile(paths.BorrowedSSID)
				writeFile(paths.InternetType, "self")
				removeFile(paths.Prompt)
				return
			}
			log.Printf("cellular reachable but still slow (%.2fMbps) - staying on borrowed hotspot", mbps)
		} else {
			log.Printf("cellular still unreachable - staying on borrowed hotspot")
		}
	}
}

// runFailoverEscalation mirrors trigger_failover: blocks until either
// a borrowed hotspot connects successfully or cellular recovers on its
// own, polling presence every failoverPollInterval and firing the SMS
// trigger both on first local confirmation and periodically regardless.
func runFailoverEscalation(ctrl Controller, cfg Config, paths FilePaths) {
	log.Printf("cellular down/slow for %d consecutive checks - entering active failover polling", consecutiveBadNeeded)
	var lastSMSSent time.Time
	phone1Sent, phone2Sent := false, false

	for {
		if ctrl.CellularReachable() {
			if mbps := ctrl.MeasureCellularMbps(); mbps >= lowSpeedThresholdMbps {
				log.Printf("cellular recovered on its own during active polling - exiting failover")
				removeFile(paths.Prompt)
				return
			}
		}

		present1 := ctrl.BTPresence(cfg.Phone1.BTMac) || ctrl.WifiPresence(cfg.Phone1.MAC)
		present2 := ctrl.BTPresence(cfg.Phone2.BTMac) || ctrl.WifiPresence(cfg.Phone2.MAC)

		switch {
		case present1 && present2:
			writeFile(paths.Prompt, "Main connection down - turn on "+cfg.Phone1.Name+"'s or "+cfg.Phone2.Name+"'s hotspot")
		case present1:
			writeFile(paths.Prompt, "Main connection down - turn on "+cfg.Phone1.Name+"'s hotspot")
		case present2:
			writeFile(paths.Prompt, "Main connection down - turn on "+cfg.Phone2.Name+"'s hotspot")
		default:
			writeFile(paths.Prompt, "Main connection down - no backup device home")
		}

		smsJustSent := false
		var shouldSend bool
		if shouldSend, phone1Sent = PresenceSMSDecision(present1, phone1Sent); shouldSend {
			ctrl.SendSMS(cfg.Phone1.Number, smsTriggerPhrase)
			log.Printf("%s confirmed present via BT/WiFi - sent trigger SMS immediately", cfg.Phone1.Name)
			smsJustSent = true
		}
		if shouldSend, phone2Sent = PresenceSMSDecision(present2, phone2Sent); shouldSend {
			ctrl.SendSMS(cfg.Phone2.Number, smsTriggerPhrase)
			log.Printf("%s confirmed present via BT/WiFi - sent trigger SMS immediately", cfg.Phone2.Name)
			smsJustSent = true
		}

		if ShouldResendPeriodicSMS(time.Now(), lastSMSSent) {
			ctrl.SendSMS(cfg.Phone1.Number, smsTriggerPhrase)
			ctrl.SendSMS(cfg.Phone2.Number, smsTriggerPhrase)
			log.Printf("periodic resend of trigger SMS to both phones")
			lastSMSSent = time.Now()
			smsJustSent = true
		}

		var connectedSSID string
		if smsJustSent {
			connectedSSID = fastPollForHotspot(ctrl, cfg)
		} else {
			connectedSSID = ctrl.ScanAndConnect(cfg.Phone1, cfg.Phone2)
		}

		if connectedSSID != "" {
			if ctrl.InternetReachableViaBorrowed() {
				log.Printf("connected to %s - internet confirmed via borrowed hotspot", connectedSSID)
				writeFile(paths.BorrowedSSID, connectedSSID)
				writeFile(paths.InternetType, "borrowed")
				removeFile(paths.Prompt)
				return
			}
			log.Printf("connected to %s but internet not reachable - forgetting and retrying", connectedSSID)
			ctrl.ForgetNetwork(connectedSSID)
		}

		time.Sleep(failoverPollInterval)
	}
}

// fastPollForHotspot is a simplified port of V1's tight post-SMS poll.
// V1 separated "rescan every 5s" from "recheck cached results every
// 1s" to get sub-second reaction without spamming real WiFi scans;
// ScanAndConnect here always does a fresh scan+8s settle per call, so
// this ends up polling roughly every ~8s rather than 1s - still bounded
// by fastPollDuration and still far faster than waiting for the next
// full failoverPollInterval (30s) cycle, just without V1's extra
// cached-results optimization.
func fastPollForHotspot(ctrl Controller, cfg Config) string {
	deadline := time.Now().Add(fastPollDuration)
	for time.Now().Before(deadline) {
		if ssid := ctrl.ScanAndConnect(cfg.Phone1, cfg.Phone2); ssid != "" {
			return ssid
		}
		time.Sleep(fastPollInterval)
	}
	return ""
}
