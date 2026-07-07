#!/system/bin/sh
# Master rule: the hotspot stays on, period - added 2026-07-07 after
# confirming the AP genuinely goes down whenever failover_monitor.sh's
# `cmd wifi connect-network` brings up station-mode Wi-Fi to reach a
# backup phone's hotspot (confirmed live: ap interface disappeared
# entirely while wlan0 was connected to "Kailash's 16 Pro Max").
#
# This chipset DOES support simultaneous STA+AP (`dumpsys wifi` shows
# "STA + AP Concurrency Supported: true" and
# config_wifiStaWithBridgedSoftApConcurrencySupported=true) - so this
# isn't a hardware limit. The shell-level `cmd wifi connect-network`
# path failover_monitor.sh uses just doesn't preserve the AP the way a
# real app using the proper concurrent-mode APIs would. Reverse-
# engineering that from shell alone isn't practical, so instead of
# fixing the root cause, this watchdog makes the symptom irrelevant:
# checks fast (every 15s, much tighter than the other 120s watchdogs)
# and immediately restarts the hotspot the instant it's found down,
# regardless of why - user toggle, failover's Wi-Fi connect, a crash,
# anything. hotspot_on_boot.sh already has the right
# stop-softap/start-softap sequence and SSID/password lookup, so this
# just re-runs that.
#
# The AP can legitimately come up as either the bridged `ap_br_wlan2`
# interface or a plain `wlan2` (confirmed both happen depending on
# opportunistic bridging state) - check for either rather than one
# fixed name.
LOG=/data/adb/dashboard/hotspot_watchdog.log
CHECK_INTERVAL=15
HEARTBEAT_EVERY=80 # ~20 min at 15s/check

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 20

CHECK_COUNT=0

while true; do
  CHECK_COUNT=$((CHECK_COUNT + 1))

  if [ ! -d /sys/class/net/wlan2 ] && [ ! -d /sys/class/net/ap_br_wlan2 ]; then
    log "hotspot AP interface missing - restarting"
    sh /data/adb/service.d/hotspot_on_boot.sh >/dev/null 2>&1
    sleep 3
    if [ -d /sys/class/net/wlan2 ] || [ -d /sys/class/net/ap_br_wlan2 ]; then
      log "hotspot restarted successfully"
    else
      log "hotspot restart attempted but AP interface still missing - will retry next cycle"
    fi
  fi

  if [ "$((CHECK_COUNT % HEARTBEAT_EVERY))" = "0" ]; then
    log "heartbeat - watchdog alive, $CHECK_COUNT checks so far"
  fi

  sleep "$CHECK_INTERVAL"
done
