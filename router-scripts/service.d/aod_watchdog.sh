#!/system/bin/sh
# Keeps the custom Router AOD (com.router.aod/.RouterDreamService)
# actually winning over Android's system Doze/ambient display, instead
# of a one-time boot fix. Confirmed 2026-07-05 that the settings this
# depends on drift back on their own during normal use:
#   - secure/screensaver_enabled reverted to 0 on its own (the master
#     switch for the whole daydream/AOD subsystem - without it, no
#     custom dream can ever activate, regardless of every other setting)
#   - the LowLightClockDreamService component-level disable also
#     reverted back to enabled on its own
# Both are silent, no user action involved - something in the OS
# (likely a periodic app-compat/settings-restore job) resets them.
# Rather than fight whatever's doing that, this just re-asserts the
# correct state on a short interval and self-heals if AOD ever loses
# out to Doze. Also doubles as a general settings-hardening watchdog
# (wireless ADB, see below) since it's already polling frequently.

LOG=/data/adb/dashboard/aod_watchdog.log
CHECK_INTERVAL=120
HEARTBEAT_EVERY=30 # ~1 hour at 120s/check - helps correlate how often
                    # drift actually happens over time, not just whether
                    # it happened at all.

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

CHECK_COUNT=0

while true; do
  CHANGED=0
  CHECK_COUNT=$((CHECK_COUNT + 1))

  if [ "$(settings get secure screensaver_enabled 2>/dev/null)" != "1" ]; then
    settings put secure screensaver_enabled 1
    log "screensaver_enabled had drifted off - re-enabled"
    CHANGED=1
  fi

  # Security audit 2026-07-05: wireless ADB was found enabled and
  # actively listening on all interfaces, reachable from any device on
  # the hotspot - a serious attack surface on a rooted device with no
  # legitimate use here. Re-assert off in case it's ever re-enabled
  # (e.g. by a stray Developer Options tap).
  if [ "$(settings get global adb_wifi_enabled 2>/dev/null)" != "0" ]; then
    settings put global adb_wifi_enabled 0
    log "adb_wifi_enabled had turned on - disabled again"
    CHANGED=1
  fi

  if [ "$(settings get secure doze_enabled 2>/dev/null)" != "0" ]; then
    settings put secure doze_enabled 0
    log "doze_enabled had drifted on - disabled again"
    CHANGED=1
  fi

  if dumpsys package com.android.systemui 2>/dev/null | grep -A5 disabledComponents | grep -q LowLightClockDreamService; then
    : # already disabled, nothing to do
  else
    pm disable com.android.systemui/.lowlightclock.LowLightClockDreamService >/dev/null 2>&1
    log "LowLightClockDreamService had re-enabled itself - disabled again"
    CHANGED=1
  fi

  WAKEFULNESS=$(dumpsys power 2>/dev/null | grep -m1 mWakefulness= | sed 's/.*mWakefulness=//')
  if [ "$WAKEFULNESS" = "Dozing" ] || [ "$WAKEFULNESS" = "Asleep" ]; then
    CURRENT_DREAM=$(dumpsys dreams 2>/dev/null | grep -m1 mCurrentDream= | grep -o "ComponentInfo{[^}]*}")
    case "$CURRENT_DREAM" in
      *router.aod*) : ;; # already ours, nothing to do
      *)
        # System Doze (or nothing) has taken over instead of our dream.
        # A fresh wake forces DreamManagerService to re-evaluate from
        # scratch, which - once the settings above are correct - lets
        # our dream win the next idle cycle instead of Doze.
        input keyevent KEYCODE_WAKEUP
        log "Doze/other dream ($CURRENT_DREAM) had taken over instead of ours - forced a fresh wake to re-trigger selection"
        CHANGED=1
        ;;
    esac
  fi

  [ "$CHANGED" = "0" ] || log "corrective action taken this pass"

  if [ "$((CHECK_COUNT % HEARTBEAT_EVERY))" = "0" ]; then
    log "heartbeat - watchdog alive, $CHECK_COUNT checks so far"
  fi

  sleep "$CHECK_INTERVAL"
done
