#!/system/bin/sh
# Kernel-level brute-force rate limit for the dashboard's basic auth,
# covering a gap fail2ban can't reach: neither public path (Cloudflare
# quick tunnel or the WireGuard/Caddy hop) preserves the real client IP
# down to busybox httpd - both arrive as 127.0.0.1 or the WireGuard peer
# address (10.66.66.1) regardless of who the actual internet client is,
# so per-source banning (what fail2ban does on the EC2/Caddy side) isn't
# possible at this layer.
#
# Two earlier approaches were tried and abandoned before this one
# (2026-07-07):
#   1. Tailing httpd.log for "response:401" lines - doesn't work at all,
#      busybox httpd never logs a rejected/failed-auth request, only
#      ones that pass auth and get served. Confirmed: 6 wrong-password
#      requests produced zero httpd.log growth.
#   2. Polling /proc/net/tcp for connections on port 8080 - too lossy at
#      any reasonable poll interval; short-lived connections (this
#      device's TCP stack reclaims them fast) can fully disappear
#      between polls, so a burst can go completely unseen.
#
# This uses iptables' `limit` module - a global token-bucket rate
# limiter entirely in-kernel at connection time, no polling gap, no
# reliance on userspace logging. First choice was the `recent` module
# (tracks hits per-source, which would have been a better fit given
# multiple legitimate viewers share the same apparent source here) but
# that's not compiled into this device's kernel at all ("Extension
# recent revision 0 not supported, missing kernel module?", confirmed
# 2026-07-07) - not fixable without a custom kernel. `limit` is a much
# older/more basic match and IS available here (verified working,
# including a live test that real excess connections get dropped once
# the burst is exceeded). It rate-limits in aggregate rather than
# per-source, which is actually fine for this case anyway - both public
# paths already collapse to a single apparent source (127.0.0.1 for
# Cloudflare, the WireGuard peer for the EC2 path) at this layer, so
# per-source tracking wouldn't have distinguished real clients here
# regardless of which module was used.
#
# Threshold: the dashboard's own JS polls speed.json every 1.5s and the
# AOD preview every 5s per open tab (~45 req/min/tab) - several family
# members with tabs open at once could plausibly hit ~150-200 req/min.
# Set well above that (300/min sustained, burst allowance of 300) so
# real usage is never touched, while still tripping fast against an
# unthrottled brute-force tool.
#
# Android's netd rewrites iptables rules on its own (network state
# changes, tethering toggles, etc.) and has been observed doing this
# elsewhere in this project - so this doesn't just set the rule once at
# boot, it re-asserts it periodically, idempotently (checks with -C
# before adding, never duplicates).

CHAIN=DASH_GATE
PORT=8080
RATE_LIMIT=300/min
BURST=300
CHECK_INTERVAL=60
LOG=/data/adb/dashboard/dashboard_ratelimit.log

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

ensure_rules() {
  iptables -N "$CHAIN" 2>/dev/null

  iptables -C "$CHAIN" -m limit --limit "$RATE_LIMIT" --limit-burst "$BURST" -j RETURN 2>/dev/null \
    || { iptables -A "$CHAIN" -m limit --limit "$RATE_LIMIT" --limit-burst "$BURST" -j RETURN; log "re-added: limit/RETURN rule"; }

  iptables -C "$CHAIN" -j DROP 2>/dev/null \
    || { iptables -A "$CHAIN" -j DROP; log "re-added: DROP rule"; }

  iptables -C INPUT -p tcp --dport "$PORT" --syn -j "$CHAIN" 2>/dev/null \
    || { iptables -I INPUT -p tcp --dport "$PORT" --syn -j "$CHAIN"; log "re-added: INPUT jump to $CHAIN"; }
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

while true; do
  ensure_rules
  sleep "$CHECK_INTERVAL"
done
