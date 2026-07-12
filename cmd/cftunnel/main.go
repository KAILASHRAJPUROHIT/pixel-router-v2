// The cftunnel service runs a named Cloudflare Tunnel pointed at the
// webproxy service, giving the dashboard a permanent real-domain URL
// alongside the existing ngrok tunnel (internal/tunnel/cmd/tunnel) -
// see internal/cftunnel's own doc comment for why this is a separate
// service rather than a replacement.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/antriva/pixel-router-v2/internal/cftunnel"
)

func main() {
	binPath := flag.String("cloudflared", "/data/adb/cloudflared", "path to the cloudflared binary")
	tunnelID := flag.String("tunnel-id", "be3365cf-866c-444f-97b0-1604d1b749e0", "the named tunnel's UUID")
	credentialsFile := flag.String("credentials-file", "/data/adb/router_v2/state/.cloudflared/be3365cf-866c-444f-97b0-1604d1b749e0.json", "path to the tunnel's credentials JSON")
	hostname := flag.String("hostname", "router.ambicdigital.in", "the hostname this tunnel serves")
	targetPort := flag.Int("target-port", 9091, "local port to expose (the webproxy service's port)")
	configPath := flag.String("config-path", "/data/adb/router_v2/state/cftunnel_config.yml", "where to write the generated ingress config")
	homeDir := flag.String("home", "/data/adb/router_v2/state", "HOME dir cloudflared reads cert.pem/credentials from")
	urlFile := flag.String("url-file", "/data/adb/router_v2/state/cftunnel_url.txt", "path to write the current tunnel URL")
	readyTimeout := flag.Duration("ready-timeout", 30*time.Second, "how long to wait for cloudflared to confirm the tunnel is up")
	flag.Parse()

	config := cftunnel.GenerateConfig(*tunnelID, *credentialsFile, *hostname, *targetPort)
	if err := os.WriteFile(*configPath, []byte(config), 0644); err != nil {
		log.Fatalf("write tunnel config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *readyTimeout)
	defer cancel()
	cmd, err := cftunnel.Launch(ctx, *binPath, *configPath, *homeDir)
	if err != nil {
		log.Fatalf("launch tunnel: %v", err)
	}

	url := "https://" + *hostname
	tmp := *urlFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(url+"\n"), 0644); err != nil {
		log.Fatalf("write url file: %v", err)
	}
	if err := os.Rename(tmp, *urlFile); err != nil {
		log.Fatalf("rename url file: %v", err)
	}
	log.Printf("tunnel URL: %s", url)

	if err := cmd.Wait(); err != nil {
		log.Fatalf("cloudflared exited: %v", err)
	}
	log.Println("cloudflared exited cleanly")
}
