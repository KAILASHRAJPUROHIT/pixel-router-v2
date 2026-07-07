#!/system/bin/sh
# Generic liveness watchdog for every long-running service.d loop script.
# Confirmed 2026-07-06: dashboard update.sh/httpd both died silently with
# no OOM-killer trace and nothing noticed until the dashboard was checked
# manually - that's a class of bug, not a one-off. Every other
# long-running script here (thermal_monitor.sh especially - the
# overheating safety net) had the exact same exposure: no supervisor, so
# a silent death is invisible until something downstream breaks. Rather
# than hand-write a bespoke per-script watchdog each time, this checks
# them all on one short interval and restarts whichever isn't running.
#
# Deliberately also covers aod_watchdog.sh/forwarder_watchdog.sh/
# dashboard_watchdog.sh themselves - those watch other things, but
# nothing was watching them.
#
# Known gap: if THIS script dies, nothing restarts it - Magisk's
# service.d only runs scripts once at boot, there's no OS-level
# supervisor underneath. Accepted limit; a full self-healing chain would
# need an init-level respawner, which this rooted-but-still-stock-ROM
# setup doesn't have.

LOG=/data/adb/dashboard/service_supervisor.log
SERVICE_DIR=/data/adb/service.d
CHECK_INTERVAL=120
HEARTBEAT_EVERY=30

# Long-running loop scripts only - one-shot boot scripts (battery_charge_
# limit.sh, conntrack.sh, dashboard_boot.sh, hotspot_on_boot.sh,
# permanent_underclock.sh, vpn_tether_routing.sh) are meant to run once
# and exit, so they're deliberately excluded here.
SCRIPTS="akd_followers_fetch.sh aod_power_governor.sh aod_status_poller.sh aod_watchdog.sh cloudflared_tunnel.sh connection_optimizer.sh dashboard_watchdog.sh failover_monitor.sh forwarder_watchdog.sh hotspot_watchdog.sh idle_photo_switcher.sh log_rotate.sh new_device_watcher.sh qos_priority.sh speed_monitor.sh thermal_monitor.sh"

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 45

CHECK_COUNT=0

while true; do
  CHANGED=0
  CHECK_COUNT=$((CHECK_COUNT + 1))

  for SCRIPT in $SCRIPTS; do
    if ! ps -ef | grep -F "sh $SERVICE_DIR/$SCRIPT" | grep -v grep >/dev/null; then
      nohup sh "$SERVICE_DIR/$SCRIPT" > /dev/null 2>&1 < /dev/null &
      log "$SCRIPT was not running - restarted"
      CHANGED=1
    fi
  done

  [ "$CHANGED" = "0" ] || log "corrective action taken this pass"

  if [ "$((CHECK_COUNT % HEARTBEAT_EVERY))" = "0" ]; then
    log "heartbeat - watchdog alive, $CHECK_COUNT checks so far"
  fi

  sleep "$CHECK_INTERVAL"
done
