#!/system/bin/sh
# Runs an arbitrary shell command as root and returns its output as
# plain text - added 2026-07-07 as a web-based equivalent of `adb
# shell` for when the phone is reachable via the dashboard but a full
# adb session isn't handy. Deliberately unrestricted (this IS a root
# shell, by design) - the only real gate is the dashboard's existing
# basic-auth layer, same trust boundary as the per-device Deny/Allow
# and custom-message features already exposed here.
#
# Bounded with `timeout` so a hung/blocking command (e.g. something
# waiting on stdin, or a runaway loop) can't wedge the CGI worker
# indefinitely - busybox httpd serves each request from a forked
# process, so a stuck command here doesn't take the whole dashboard
# down, but it would leave that request hanging forever without this.
MAX_SECONDS=20
LOG=/data/adb/dashboard/debug_console.log

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# Same decode_field() pattern as message.cgi/rename_device.cgi/toggle_block.cgi.
decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

CMD=$(decode_field cmd)

echo "Content-Type: text/plain; charset=utf-8"
echo ""

if [ -z "$CMD" ]; then
  echo "(empty command)"
  exit 0
fi

# Audit trail - added 2026-07-07 security review. This CGI runs as root
# with no restriction on what it executes, and busybox httpd's basic
# auth is the ONLY gate in front of it - worse, the Cloudflare quick
# tunnel path (cloudflared_tunnel.sh, a separate public *.trycloudflare.com
# URL) reaches this same script and isn't covered by the EC2 box's
# fail2ban jail at all (that only watches Caddy's access log for the
# antrivarouter.duckdns.org path). A logged-then-executed record here
# is the only after-the-fact visibility into misuse via either path,
# since CGI scripts never even run for a *rejected* basic-auth attempt
# (httpd gates that before invoking anything), so there's no way to
# rate-limit or lock out failed attempts from inside this script itself.
log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') [${REMOTE_ADDR:-unknown}] $1" >> "$LOG"
}
log "CMD: $CMD"
OUTPUT=$(timeout "$MAX_SECONDS" su -c "$CMD" 2>&1)
EXIT_CODE=$?
log "EXIT=$EXIT_CODE"
printf '%s' "$OUTPUT"
