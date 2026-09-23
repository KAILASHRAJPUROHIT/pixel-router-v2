#!/system/bin/sh
# cloudflared can end up alive but stuck - the process itself never
# exits, so cloudflared_tunnel.sh's own restart-on-exit loop
# (`wait "$CF_PID"` -> restart) never fires, yet the tunnel silently
# stops being reachable. Confirmed live 2026-07-07: the process had
# been running for 94+ minutes, its log had completely stopped growing,
# and the public tunnel URL was unresolvable/unreachable the entire
# time - a real, silent outage on this path with the primary
# DuckDNS/Caddy path unaffected.
#
# This watches for that specific failure mode - not "is the process
# running" (it always is, that's the whole problem) but "is it still
# doing anything" - and kills it when it looks hung, letting
# cloudflared_tunnel.sh's existing restart loop take over from there
# rather than duplicating that start/parse-URL logic here.
STATE_DIR=/data/adb/dashboard
LOG="$STATE_DIR/cloudflared_health_check.log"
CF_LOG="$STATE_DIR/cloudflared.log"
# Cloudflare's edge normally produces some log line (a retry, a
# reconnect, a curve-preference negotiation) at least every couple of
# minutes even when things are going wrong - true silence this long
# while the process is still alive is the hang signature, not just a
# quiet period.
STALE_THRESHOLD_SECONDS=600
CHECK_INTERVAL=300

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 60

while true; do
  CF_PID=$(ps -ef | grep "cloudflared tunnel" | grep -v grep | awk '{print $2}')
  if [ -n "$CF_PID" ] && [ -f "$CF_LOG" ]; then
    LOG_MTIME=$(stat -c %Y "$CF_LOG" 2>/dev/null)
    NOW=$(date +%s)
    if [ -n "$LOG_MTIME" ] && [ "$((NOW - LOG_MTIME))" -ge "$STALE_THRESHOLD_SECONDS" ]; then
      log "cloudflared (pid $CF_PID) looks hung - log hasn't grown in $((NOW - LOG_MTIME))s, killing so the wrapper restarts it"
      kill -9 "$CF_PID" 2>/dev/null
    fi
  fi
  sleep "$CHECK_INTERVAL"
done
