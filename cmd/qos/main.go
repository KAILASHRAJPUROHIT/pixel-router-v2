// Command qos ports V1's qos_priority.sh.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/qos"
)

func main() {
	priorityMAC := flag.String("priority-mac", "", "MAC address of the device to give egress priority (empty disables)")
	interval := flag.Duration("check-interval", 2*time.Minute, "how often to re-check WAN interface/priority IP/qdisc state")
	flag.Parse()

	if *priorityMAC == "" {
		log.Println("--priority-mac not set - nothing to prioritize, exiting")
		return
	}

	ctrl := qos.RealController{}
	st := &qos.State{}
	wasConnected := true // avoid a spurious "disconnected" log on the very first tick

	for {
		result := qos.Tick(ctrl, st, *priorityMAC)
		if result.Applied {
			log.Printf("QoS applied on %s: priority device mapped to band 1:1 (%s)", st.LastWanIf, result.Reason)
		}
		if !result.PriorityConnected && wasConnected {
			log.Println("priority device disconnected - previous rules left in place until it reconnects")
		}
		wasConnected = result.PriorityConnected
		time.Sleep(*interval)
	}
}
