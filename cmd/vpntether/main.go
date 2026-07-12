// Command vpntether ports V1's vpn_tether_routing.sh.
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/antriva/pixel-router-v2/internal/vpntether"
)

func main() {
	interval := flag.Duration("check-interval", 30*time.Second, "how often to re-evaluate internet_type/tunnel-alive state - kept longer than the WireGuard config's own PersistentKeepalive so at least one keepalive round-trip is spanned per check")
	tunnelStatusPath := flag.String("tunnel-status-path", "/data/adb/router_v2/state/tunnel_status.txt", "path to write the tunnel's alive/dead/not_present status, for the dashboard to read - see V1's incident where a silently-dead tunnel went unnoticed for lack of exactly this visibility")
	flag.Parse()

	ctrl := vpntether.RealController{}
	st := &vpntether.State{}

	for {
		result := vpntether.Tick(ctrl, st, time.Now())
		if result.Err != nil {
			log.Printf("tick error: %v", result.Err)
		}
		if result.ReconnectAttempted {
			log.Println("tunnel dead for a sustained streak - sent WireGuard reconnect broadcast")
		}
		if result.BypassChanged {
			if result.NowBypassing {
				log.Println("on own cellular with a live tunnel - routing hotspot traffic through it (Airtel tethering-detection bypass active)")
			} else {
				log.Println("no longer bypass-routing (either off own cellular, or the tunnel went dead) - direct routing in effect")
			}
		}

		status := "not_present"
		if _, err := os.Stat("/sys/class/net/tun0"); err == nil {
			if result.TunnelAlive {
				status = "alive"
			} else {
				status = "dead"
			}
		}
		os.WriteFile(*tunnelStatusPath, []byte(status), 0644)

		time.Sleep(*interval)
	}
}
