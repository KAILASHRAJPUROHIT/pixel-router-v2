#!/system/bin/sh
# Keeps the local dashboard (update.sh regenerating index.html + busybox
# httpd serving it on :8080) alive - this is what both the Cloudflare
# quick tunnel and the WireGuard/Caddy/DuckDNS path ultimately depend on.
# Confirmed 2026-07-06 that both processes can die independently (no
# crash loop, no auto-restart) and nothing else notices - the dashboard
# was unreachable (502 from Caddy) until a manual check caught it. This
# just checks both are alive on a short interval and restarts whichever
# died, same pattern as aod_watchdog.sh/forwarder_watchdog.sh.

LOG=/data/adb/dashboard/dashboard_watchdog.log
CHECK_INTERVAL=120
HEARTBEAT_EVERY=30
UPDATE_SCRIPT=/data/adb/dashboard/update.sh
HTTPD_ROOT=/data/local/tmp/dashboard
HTTPD_CONF=/data/local/tmp/dashboard/httpd.conf
HTTPD_PORT=8080

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

CHECK_COUNT=0

while true; do
  CHANGED=0
  CHECK_COUNT=$((CHECK_COUNT + 1))

  if ! ps -ef | grep -F "sh $UPDATE_SCRIPT" | grep -v grep >/dev/null; then
    nohup sh "$UPDATE_SCRIPT" > /data/adb/dashboard/update.log 2>&1 < /dev/null &
    log "update.sh was not running - restarted"
    CHANGED=1
  fi

  if ! ps -ef | grep -F "busybox httpd" | grep -F "$HTTPD_PORT" | grep -v grep >/dev/null; then
    nohup /data/adb/magisk/busybox httpd -f -vv -p "$HTTPD_PORT" -h "$HTTPD_ROOT" -c "$HTTPD_CONF" > /data/adb/dashboard/httpd.log 2>&1 < /dev/null &
    log "busybox httpd was not running - restarted"
    CHANGED=1
  fi

  [ "$CHANGED" = "0" ] || log "corrective action taken this pass"

  if [ "$((CHECK_COUNT % HEARTBEAT_EVERY))" = "0" ]; then
    log "heartbeat - watchdog alive, $CHECK_COUNT checks so far"
  fi

  sleep "$CHECK_INTERVAL"
done
