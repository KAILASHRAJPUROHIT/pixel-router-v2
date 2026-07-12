// Command connmonitor ports V1's connection_optimizer.sh,
// connection_stability_poller.sh, and airtel_detection_safety_poller.sh
// into one process with two independent cadences.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/connmonitor"
)

func main() {
	stabilityInterval := flag.Duration("stability-interval", connmonitor.StabilityInterval, "how often to check ping/signal stability and settings drift")
	detectionInterval := flag.Duration("detection-interval", connmonitor.DetectionSafetyInterval, "how often to verify TTL-proxy detection protection is intact")
	flag.Parse()

	ctrl := connmonitor.RealController{}

	go func() {
		for {
			result := connmonitor.TickDetectionSafety(ctrl, func() { time.Sleep(5 * time.Second) })
			if result.Err != nil {
				log.Printf("detection-safety tick error: %v", result.Err)
			}
			if result.RestartAttempted {
				if result.RestartConfirmed {
					log.Println("TTL-proxy protection was not intact - restarted, now confirmed intact")
				} else {
					log.Println("TTL-proxy protection was not intact - restart attempted but still not confirmed, will retry")
				}
			}
			if result.HoppatchFlagCreated {
				log.Println("hoppatch disable flag was missing - re-created")
			}
			time.Sleep(*detectionInterval)
		}
	}()

	for {
		result := connmonitor.TickStability(ctrl)
		if len(result.DriftFixed) > 0 {
			log.Printf("settings drift corrected: %v", result.DriftFixed)
		}
		if result.ManualSelection {
			log.Println("WARNING: network selection has switched to MANUAL - needs a manual check in Settings, no safe automated fix")
		}
		if result.Unstable {
			status := "reconnect attempted"
			if result.Err != nil {
				status = "reconnect attempt errored: " + result.Err.Error()
			}
			log.Printf("unstable connection detected (%s) - %s", result.Reason, status)
		}
		time.Sleep(*stabilityInterval)
	}
}
