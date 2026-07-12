// The webproxy service is the single thing the tunnel service exposes
// publicly - it fans a request out to pixel-router-v2's own dashboard,
// AKD, or local-router by path prefix, so all three projects can share
// the one free static tunnel domain instead of each needing their own.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/antriva/pixel-router-v2/internal/webproxy"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9091", "listen address - loopback only, the tunnel service reaches in from here")
	defaultTarget := flag.String("default-target", "http://127.0.0.1:9090", "backend for unmatched paths (pixel-router-v2's own dashboard)")
	akdTarget := flag.String("akd-target", "http://10.195.140.200:8000", "backend for /akd/* (AKD's FastAPI dashboard, PC-hosted)")
	localRouterTarget := flag.String("local-router-target", "http://10.195.140.200:8787", "backend for /local-router/* (PC-hosted)")
	flag.Parse()

	routes := []webproxy.Route{
		{Prefix: "/akd", Target: *akdTarget},
		{Prefix: "/local-router", Target: *localRouterTarget},
	}

	handler, err := webproxy.NewHandler(routes, *defaultTarget)
	if err != nil {
		log.Fatalf("build proxy handler: %v", err)
	}

	log.Printf("webproxy listening on %s (default=%s akd=%s local-router=%s)", *addr, *defaultTarget, *akdTarget, *localRouterTarget)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatalf("webproxy: %v", err)
	}
}
