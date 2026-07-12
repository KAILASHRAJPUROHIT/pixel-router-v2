package main

import (
	"flag"
	"log"
	"strings"
	"time"

	"github.com/antriva/pixel-router-v2/internal/forwarder"
)

func main() {
	pkg := flag.String("package", "cn.ppps.forwarder", "SMS/call forwarder app package name")
	permissions := flag.String("permissions", "RECEIVE_SMS,READ_SMS,SEND_SMS", "comma-separated permissions to enforce")
	checkInterval := flag.Duration("check-interval", 20*time.Second, "how often to check health")
	flag.Parse()

	ctrl := forwarder.RealController{Package: *pkg}
	perms := strings.Split(*permissions, ",")

	for {
		fixed, err := forwarder.EnsureHealthy(ctrl, perms)
		if err != nil {
			log.Printf("health check error: %v", err)
		}
		for _, f := range fixed {
			log.Println(f)
		}
		time.Sleep(*checkInterval)
	}
}
