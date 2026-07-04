#!/system/bin/sh
# Exposes the local router dashboard (127.0.0.1:8080) to the public
# internet via a Cloudflare quick tunnel - no account/domain needed.
# The assigned *.trycloudflare.com URL is stable for as long as this
# process keeps running, and only changes if the process (or the whole
# device) restarts - which for this always-on router only happens on a
# deliberate reboot, so in practice the URL is effectively long-lived.
#
# Basic auth is enforced by busybox httpd itself (see dashboard_htpasswd
# setup), NOT by cloudflared - the tunnel is just a dumb pipe.

BIN=/data/adb/cloudflared
STATE_DIR=/data/adb/dashboard
URL_FILE="$STATE_DIR/tunnel_url.txt"
LOG="$STATE_DIR/cloudflared.log"

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 20

# cloudflared prints the assigned quick-tunnel URL to stderr on startup -
# capture it once into a file so it can be read without needing to
# re-parse the live log every time.
> "$URL_FILE"

# Statically-linked Go binaries on Android can't find a CA bundle at any
# of Go's default Linux paths (Android doesn't ship one there) - without
# this every TLS handshake cloudflared makes fails with "certificate
# signed by unknown authority", confirmed during testing. Android's own
# system CA store at this path is already in a format (individual PEM
# files per cert) Go's loader can read directly.
export SSL_CERT_DIR=/system/etc/security/cacerts

"$BIN" tunnel --url http://127.0.0.1:8080 --no-autoupdate >> "$LOG" 2>&1 &
CF_PID=$!
log "cloudflared started, pid=$CF_PID"

# Poll the log for the assigned URL (appears within a few seconds of start).
# Explicitly exclude api.trycloudflare.com - cloudflared's own request
# line ("Requesting new quick Tunnel...") and its TLS-failure retry text
# both mention that fixed hostname, which the naive pattern also matches
# and would wrongly report as "the" tunnel URL.
for i in $(seq 1 30); do
  URL=$(grep -o 'https://[a-zA-Z0-9-]*\.trycloudflare\.com' "$LOG" | grep -v '^https://api\.trycloudflare\.com$' | tail -1)
  if [ -n "$URL" ]; then
    echo "$URL" > "$URL_FILE"
    log "tunnel URL assigned: $URL"
    break
  fi
  sleep 2
done

wait "$CF_PID"
log "cloudflared exited unexpectedly"
