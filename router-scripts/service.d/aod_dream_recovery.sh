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
LOG=/data/adb/dashboard/aod_dream_recovery.log
CHECK_INTERVAL=5
HEARTBEAT_EVERY=360  # ~30 min at 5s/check

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

while true; do
  CHECK_COUNT=$((CHECK_COUNT + 1))
  WAKEFULNESS=$(dumpsys power 2>/dev/null | grep -m1 mWakefulness= | sed 's/.*mWakefulness=//')
  if [ "$WAKEFULNESS" = "Dozing" ] || [ "$WAKEFULNESS" = "Asleep" ]; then
    CURRENT_DREAM=$(dumpsys dreams 2>/dev/null | grep -m1 mCurrentDream= | grep -o "ComponentInfo{[^}]*}")
    case "$CURRENT_DREAM" in
      *router.aod*) : ;; # already ours, nothing to do
      *)
        cmd dreams start-dreaming >/dev/null 2>&1
        RECOVERIES_THIS_HOUR=$((RECOVERIES_THIS_HOUR + 1))
        log "dream wasn't ours ($CURRENT_DREAM) - ran 'cmd dreams start-dreaming'"
        ;;
    esac
  fi

  NOW=$(date +%s)
  if [ "$((NOW - LAST_HOUR_RESET))" -ge 3600 ]; then
    log "heartbeat - $RECOVERIES_THIS_HOUR recoveries in the last hour, $CHECK_COUNT checks total"
    RECOVERIES_THIS_HOUR=0
    LAST_HOUR_RESET=$NOW
  fi

  sleep "$CHECK_INTERVAL"
done
