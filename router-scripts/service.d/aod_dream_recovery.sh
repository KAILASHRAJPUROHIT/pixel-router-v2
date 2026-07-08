#!/system/bin/sh
# Fast dream-recovery watchdog - added 2026-07-08 after finding the
# real root cause of a recurring "AOD not working" report. Android's
# own SystemUI periodically (observed roughly every 10-15s while
# charging) attempts to switch the active dream to
# com.android.systemui.lowlightclock.LowLightClockDreamService
# (logcat: reason='set system dream component') - and this happens
# REGARDLESS of that component already being pm-disabled (confirmed
# live: it's genuinely in PackageManager's disabledComponents list, yet
# the switch attempt still tears down our currently-running dream via
# "stop previous dream" *before* discovering the bind fails ("Unable to
# bind dream service" / "bindService failed"), leaving nothing dreaming
# and waking the device (WAKE_REASON_DREAM_FINISHED). This is what
# actually looked like "AOD not working" to the naked eye - the screen
# flickering awake every ~10s with the stats display never staying up
# more than a few seconds at a time.
#
# aod_watchdog.sh's existing fix (force-stop + toggle
# screensaver_enabled + wake) is both too slow at its 120s interval to
# keep up with a ~10-15s disruption cadence, and heavier than necessary
# for this specific case - `cmd dreams start-dreaming` (confirmed live
# 2026-07-08: cleanly and immediately restarts the currently-configured
# dream, i.e. ours, with none of that heavier machinery) is a much
# faster, lighter reaction. This runs on its own tight interval just
# for that; aod_watchdog.sh's slower loop stays as-is for the genuinely
# rarer settings-drift cases (screensaver_enabled reverting to 0 at the
# master-switch level, which this lighter command alone can't fix).
#
# Also absorbed aod_status_poller.sh's job 2026-07-08 (retired, see
# README) - that script polled dumpsys power/dreams every single
# second forever, running the exact same class of check this script
# already does at 5s, plus a heavier redundant fix action. Two watchdogs
# independently hammering the same dumpsys calls on different clocks
# was pure waste, not extra safety; this is now the one fast-reaction
# layer, aod_watchdog.sh the one slow settings-drift layer.
#
# Heat/network budget, same adaptive-backoff shape as hotspot_manager.sh:
# now that the actual root cause of the flicker (a disabled app
# verifier blocking RouterDreamService from launching at all - see
# aod_watchdog.sh) is fixed, this fast interval is a safety net for a
# rare case, not a constant workaround for a constant problem. No
# reason to poll dumpsys twice a second forever when nothing's wrong;
# back off while healthy, snap back to fast the moment something isn't.
LOG=/data/adb/dashboard/aod_dream_recovery.log
STATUS_FILE=/data/adb/dashboard/aod_live_status.txt
MIN_INTERVAL=5
MAX_INTERVAL=60
BACKOFF_AFTER_HEALTHY_STREAK=6
HEARTBEAT_EVERY=360  # ~30 min at min interval

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 20

CHECK_COUNT=0
RECOVERIES_THIS_HOUR=0
LAST_HOUR_RESET=$(date +%s)
HEALTHY_STREAK=0
CURRENT_INTERVAL=$MIN_INTERVAL

while true; do
  CHECK_COUNT=$((CHECK_COUNT + 1))
  WAKEFULNESS=$(dumpsys power 2>/dev/null | grep -m1 mWakefulness= | sed 's/.*mWakefulness=//')
  MISMATCH=0
  if [ "$WAKEFULNESS" = "Dozing" ] || [ "$WAKEFULNESS" = "Asleep" ]; then
    CURRENT_DREAM=$(dumpsys dreams 2>/dev/null | grep -m1 mCurrentDream= | grep -o "ComponentInfo{[^}]*}")
    case "$CURRENT_DREAM" in
      *router.aod*) echo "on $(date +%s)" > "$STATUS_FILE" ;; # already ours, nothing to do
      *)
        MISMATCH=1
        cmd dreams start-dreaming >/dev/null 2>&1
        RECOVERIES_THIS_HOUR=$((RECOVERIES_THIS_HOUR + 1))
        log "dream wasn't ours ($CURRENT_DREAM) - ran 'cmd dreams start-dreaming'"
        echo "off $(date +%s)" > "$STATUS_FILE"
        ;;
    esac
  else
    echo "awake $(date +%s)" > "$STATUS_FILE"
  fi

  if [ "$MISMATCH" = "1" ]; then
    HEALTHY_STREAK=0
    CURRENT_INTERVAL=$MIN_INTERVAL
  else
    HEALTHY_STREAK=$((HEALTHY_STREAK + 1))
    if [ "$HEALTHY_STREAK" -ge "$BACKOFF_AFTER_HEALTHY_STREAK" ]; then
      HEALTHY_STREAK=0
      CURRENT_INTERVAL=$((CURRENT_INTERVAL * 2))
      [ "$CURRENT_INTERVAL" -le "$MAX_INTERVAL" ] || CURRENT_INTERVAL=$MAX_INTERVAL
    fi
  fi

  NOW=$(date +%s)
  if [ "$((NOW - LAST_HOUR_RESET))" -ge 3600 ]; then
    log "heartbeat - $RECOVERIES_THIS_HOUR recoveries in the last hour, $CHECK_COUNT checks total, current interval ${CURRENT_INTERVAL}s"
    RECOVERIES_THIS_HOUR=0
    LAST_HOUR_RESET=$NOW
  fi

  sleep "$CURRENT_INTERVAL"
done
