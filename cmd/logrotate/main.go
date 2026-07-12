// Command logrotate ports V1's log_rotate.sh, applied to V2's own log
// directory.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/antriva/pixel-router-v2/internal/logrotate"
)

func main() {
	dir := flag.String("dir", "/data/adb/router_v2/logs", "directory of *.log files to rotate")
	interval := flag.Duration("check-interval", time.Hour, "how often to check for oversized logs")
	flag.Parse()

	for {
		rotated, err := logrotate.RotateDir(*dir)
		if err != nil {
			log.Printf("rotate error: %v", err)
		} else if len(rotated) > 0 {
			log.Printf("rotated: %v", rotated)
		}
		time.Sleep(*interval)
	}
}
