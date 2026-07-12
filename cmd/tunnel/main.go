// The tunnel service launches ngrok bound to a reserved static domain,
// confirms it came up, persists the (already-known, fixed) URL for the
// dashboard to read, then waits on the child. If ngrok dies, this
// process exits too - the supervisor restarts the whole service fresh.
//
// Unlike the previous cloudflared-based quick-tunnel design, the URL
// here never changes across restarts - it's a domain reserved on the
// ngrok account, not one assigned fresh per launch - so there's no
// stale-URL race to guard against on the read side.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/antriva/pixel-router-v2/internal/tunnel"
)

func main() {
	binPath := flag.String("ngrok", "/data/adb/ngrok", "path to the ngrok binary")
	url := flag.String("url", "https://turban-tried-snarl.ngrok-free.dev", "the reserved static ngrok domain")
	targetPort := flag.Int("target-port", 9091, "local port to expose (the webproxy service's port, not the webserver directly)")
	homeDir := flag.String("home", "/data/adb/router_v2/state", "HOME dir ngrok reads its authtoken/config from")
	urlFile := flag.String("url-file", "/data/adb/router_v2/state/tunnel_url.txt", "path to write the current tunnel URL")
	readyTimeout := flag.Duration("ready-timeout", 30*time.Second, "how long to wait for ngrok to confirm the tunnel is up")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *readyTimeout)
	defer cancel()
	cmd, err := tunnel.Launch(ctx, *binPath, *url, *homeDir, *targetPort)
	if err != nil {
		log.Fatalf("launch tunnel: %v", err)
	}

	// Same atomic-write convention as the supervisor's status file -
	// temp file + rename, so a reader (the dashboard) never sees a
	// half-written URL.
	tmp := *urlFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(*url+"\n"), 0644); err != nil {
		log.Fatalf("write url file: %v", err)
	}
	if err := os.Rename(tmp, *urlFile); err != nil {
		log.Fatalf("rename url file: %v", err)
	}
	log.Printf("tunnel URL: %s", *url)

	if err := cmd.Wait(); err != nil {
		log.Fatalf("ngrok exited: %v", err)
	}
	log.Println("ngrok exited cleanly")
}
