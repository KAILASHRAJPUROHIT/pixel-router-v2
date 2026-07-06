#!/system/bin/sh
# Fast-reaction companion to aod_watchdog.sh (which only checks every
# 120s, so a blackout could sit unfixed for up to 2 minutes). This
# polls once a second and applies the same proven force-stop+toggle+
# wake fix immediately when Doze (or nothing) has taken over the
# dream slot instead of our AOD, added 2026-07-06 after another
# real blackout was reported.
#
# Kept lightweight on purpose: only two cheap `dumpsys` calls per
# second in the common case (nothing wrong), and the second call
# (dreams) only runs at all while the device is actually Dozing/
# Asleep - it's a no-op the rest of the time. The expensive
# corrective sequence only runs on the rare tick where something's
# actually broken, not every second.
LOG=/data/adb/dashboard/aod_status_poller.log
STATUS_FILE=/data/adb/dashboard/aod_live_status.txt
CHECK_INTERVAL=1

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

LAST_STATE=""

while true; do
  WAKEFULNESS=$(dumpsys power 2>/dev/null | grep -m1 mWakefulness= | sed 's/.*mWakefulness=//')

  if [ "$WAKEFULNESS" = "Dozing" ] || [ "$WAKEFULNESS" = "Asleep" ]; then
    CURRENT_DREAM=$(dumpsys dreams 2>/dev/null | grep -m1 mCurrentDream= | grep -o "ComponentInfo{[^}]*}")
    case "$CURRENT_DREAM" in
      *router.aod*) STATE="on" ;;
      *) STATE="off" ;;
    esac
  else
    STATE="awake"
  fi

  echo "$STATE $(date +%s)" > "$STATUS_FILE"

  if [ "$STATE" = "off" ]; then
    am force-stop com.router.aod >/dev/null 2>&1
    settings put secure screensaver_enabled 0
    sleep 1
    settings put secure screensaver_enabled 1
    input keyevent KEYCODE_WAKEUP
    [ "$LAST_STATE" = "off" ] || log "AOD went black ($CURRENT_DREAM) - force-stopped + toggled screensaver + fresh wake"
  fi

  LAST_STATE="$STATE"
  sleep "$CHECK_INTERVAL"
done
