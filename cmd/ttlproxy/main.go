package main

import (
	"flag"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/antriva/pixel-router-v2/internal/ttlproxy"
)

func main() {
	port := flag.Int("port", 12346, "local TPROXY listen port")
	mark := flag.String("mark", "0x2", "fwmark used for policy routing")
	table := flag.String("table", "101", "policy routing table id")
	// Confirmed live 2026-07-12 - this is a bigger fix than just adding
	// wlan3: this device's kernel has no br_netfilter module at all (no
	// /proc/sys/net/bridge/bridge-nf-call-iptables file exists), so
	// iptables structurally CANNOT see bridged traffic via the physical
	// radio port names (wlan2/wlan3) at all - a rule matching `-i wlan2`
	// never matches a single packet of real bridged client traffic,
	// verified by a live counter test (0 hits after generating fresh
	// traffic, vs 62 packets immediately matched on `-i ap_br_wlan2`).
	// This means the ENTIRE TTL-normalization defense had silently never
	// intercepted a single bridged-mode client connection.
	//
	// The correct interface depends on which band mode is currently
	// active (bridged dual-band creates ap_br_wlan2; a single-band-only
	// config may use the raw radio directly with no bridge at all - see
	// V1's own detect_ap_if() fallback logic, which this project's
	// hotspot package still mirrors) - rather than dynamically detecting
	// and having to restart this service on every band-mode toggle, the
	// full superset is listed here: whichever names don't currently
	// exist as real interfaces simply match zero packets, harmlessly.
	interfaces := flag.String("interfaces", "ap_br_wlan2,wlan2,wlan3,ncm0", "comma-separated hotspot/tether interfaces - superset covering both bridged and single-band-only topologies")
	enableV6 := flag.Bool("ipv6", true, "also set up the IPv6 TPROXY path")
	flag.Parse()

	cfg := ttlproxy.SetupConfig{
		ProxyPort:  *port,
		Mark:       *mark,
		TableID:    *table,
		Interfaces: strings.Split(*interfaces, ","),
	}

	if err := ttlproxy.SetupIPv4(cfg); err != nil {
		log.Fatalf("IPv4 setup: %v", err)
	}
	log.Println("IPv4 TPROXY rules applied")

	if *enableV6 {
		if err := ttlproxy.SetupIPv6(cfg); err != nil {
			log.Printf("IPv6 setup failed (continuing IPv4-only): %v", err)
		} else {
			log.Println("IPv6 TPROXY rules applied")
		}
	}

	dialer := ttlproxy.NewNetDialer(10 * time.Second)
	proxy := ttlproxy.New(dialer, log.Default())

	l4, err := ttlproxy.ListenTransparent("tcp4", ":"+strconv.Itoa(*port))
	if err != nil {
		log.Fatalf("listen tcp4: %v", err)
	}
	log.Printf("listening (transparent) tcp4 :%d", *port)
	go func() {
		if err := proxy.Serve(l4); err != nil {
			log.Printf("tcp4 serve exited: %v", err)
		}
	}()

	if *enableV6 {
		l6, err := ttlproxy.ListenTransparent("tcp6", ":"+strconv.Itoa(*port))
		if err != nil {
			log.Printf("listen tcp6 failed (continuing IPv4-only): %v", err)
		} else {
			log.Printf("listening (transparent) tcp6 :%d", *port)
			go func() {
				if err := proxy.Serve(l6); err != nil {
					log.Printf("tcp6 serve exited: %v", err)
				}
			}()
		}
	}

	select {}
}
