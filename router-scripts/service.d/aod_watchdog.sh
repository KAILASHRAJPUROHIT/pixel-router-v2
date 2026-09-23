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

  # Root cause found 2026-07-08 of the every-40-90s dream flicker that
  # aod_dream_recovery.sh/aod_watchdog.sh's mismatch-recovery logic was
  # only ever reacting to, never preventing: screensaver_components (the
  # allowlist) had our RouterDreamService, but the SEPARATE
  # screensaver_default_component setting - what DreamManagerService
  # actually launches for its own periodic mDreamsActivatedOnChargeByDefault
  # trigger while charging, independent of the allowlist - was still
  # deskclock's Screensaver. That's what was tearing our dream down every
  # cycle, not a rogue app; the actual target app is even pm-disabled, so
  # every attempt silently failed after already tearing ours down.
  if [ "$(settings get secure screensaver_default_component 2>/dev/null)" != "com.router.aod/.RouterDreamService" ]; then
    settings put secure screensaver_default_component com.router.aod/.RouterDreamService
    log "screensaver_default_component had drifted off RouterDreamService - reset"
    CHANGED=1
  fi

  # Actual root cause found 2026-07-08, after the fix above alone didn't
  # stop the flicker: logcat at the exact moment of every dream teardown
  # showed "DreamValidator: Dream ComponentInfo{com.router.aod/...} does
  # not exist on user 0" - RouterDreamService was never losing to a rival
  # dream, it could never start at all. Traced via install-time logcat to
  # "VerifierController: Failed to bind to service ... pkg=com.google.
  # android.verifier" - this OS build (very new, sdk 37) gates a locally-
  # installed/updated app's components behind a mandatory install-time
  # developer-verification handshake; if the verifier itself is disabled,
  # the app's manifest still parses into the resolver table (so it LOOKS
  # registered) but its components never fully activate, so every launch
  # attempt (by us, by DreamManagerService, by am start) fails with
  # "Error type 3: Activity class ... does not exist". The earlier
  # Google-app debloat pass disabled this as "non-essential" - it wasn't,
  # on this OS version. Also affects any other locally-sideloaded app
  # (confirmed com.wireguard.android hit the identical failure).
  if [ "$(settings get global package_verifier_enable 2>/dev/null)" = "0" ] || \
     ! dumpsys package com.google.android.verifier 2>/dev/null | grep -q "enabled=1"; then
    pm enable com.google.android.verifier >/dev/null 2>&1
    settings put global package_verifier_enable 1
    log "com.google.android.verifier had drifted disabled - re-enabled (blocks launch of any sideloaded app on this OS build, not just AOD)"
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

  # Heat-reduction pass 2026-07-07: NFC and adaptive brightness are both
  # unused/counterproductive on a stationary router with no NFC use case
  # and a fixed AOD display that shouldn't get brighter just because a
  # room is sunlit - re-assert both off/fixed here in case either drifts
  # back (e.g. an OS update re-enabling NFC by default), same pattern as
  # every other drift check in this loop.
  if [ "$(dumpsys nfc 2>/dev/null | grep -m1 mState= | sed 's/.*mState=//')" != "off" ]; then
    svc nfc disable >/dev/null 2>&1
    log "NFC had turned back on - disabled again"
    CHANGED=1
  fi
  if [ "$(settings get system screen_brightness_mode 2>/dev/null)" != "0" ]; then
    settings put system screen_brightness_mode 0
    log "adaptive brightness had turned back on - disabled again"
    CHANGED=1
  fi

  # battery_charge_limit.sh only sets this once at boot - re-assert
  # here too, same pattern as the other drift checks in this loop,
  # since we don't yet have full confidence it holds reliably long-term
  # (2026-07-06 testing: this node genuinely caps charging, but nothing
  # was re-checking it between boots until now).
  CHARGE_LIMIT_NODE=/sys/class/power_supply/battery/charge_limit
  if [ -w "$CHARGE_LIMIT_NODE" ] && [ "$(cat "$CHARGE_LIMIT_NODE" 2>/dev/null)" != "80" ]; then
    echo 80 > "$CHARGE_LIMIT_NODE"
    log "charge_limit had drifted - reset to 80"
    CHANGED=1
  fi

  if dumpsys package com.android.systemui 2>/dev/null | grep -A5 disabledComponents | grep -q LowLightClockDreamService; then
    : # already disabled, nothing to do
  else
    pm disable com.android.systemui/.lowlightclock.LowLightClockDreamService >/dev/null 2>&1
    log "LowLightClockDreamService had re-enabled itself - disabled again"
    CHANGED=1
  fi

  # This device is a dedicated always-on-charge router, not a daily
  # driver phone - there's no legitimate use for the system's own
  # ambient-display Doze here, and confirmed 2026-07-06 that it had
  # been silently winning over our dream every few minutes all evening
  # despite doze_enabled=0 (that setting alone doesn't stop
  # DreamManagerService from selecting DozeService specifically during
  # the Dozing/Asleep wakefulness states). Disabling the component
  # outright means it can never be a dream candidate at all, regardless
  # of any settings toggle - re-asserted here the same way as
  # LowLightClockDreamService in case an OS update ever re-enables it.
  if dumpsys package com.android.systemui 2>/dev/null | grep -A5 disabledComponents | grep -q "doze.DozeService"; then
    : # already disabled, nothing to do
  else
    pm disable com.android.systemui/.doze.DozeService >/dev/null 2>&1
    log "DozeService had re-enabled itself - disabled again"
    CHANGED=1
  fi

  WAKEFULNESS=$(dumpsys power 2>/dev/null | grep -m1 mWakefulness= | sed 's/.*mWakefulness=//')
  if [ "$WAKEFULNESS" = "Dozing" ] || [ "$WAKEFULNESS" = "Asleep" ]; then
    CURRENT_DREAM=$(dumpsys dreams 2>/dev/null | grep -m1 mCurrentDream= | grep -o "ComponentInfo{[^}]*}")
    case "$CURRENT_DREAM" in
      *router.aod*) : ;; # already ours, nothing to do
      *)
        # Re-verify after a brief settle delay before acting - confirmed
        # live 2026-07-08 that this check can catch a perfectly normal,
        # momentary dream-transition gap (mCurrentDream briefly empty
        # while one dream stops and the next starts, not Doze actually
        # winning) and misreport it as a takeover. Worse, the force-stop
        # fix below is itself a dream-stop/restart cycle, so a false
        # positive here can get caught again by a LATER check mid-fix,
        # creating a self-perpetuating churn - directly observed: a dream
        # starting normally, then getting stopped again ~2s later with
        # reason='stop previous dream', repeating across 5+ incidents in
        # 4 hours. Only escalate to the disruptive fix if the mismatch
        # is still there 5s later, which a real takeover (as opposed to
        # a transition blip) will be.
        sleep 5
        CURRENT_DREAM=$(dumpsys dreams 2>/dev/null | grep -m1 mCurrentDream= | grep -o "ComponentInfo{[^}]*}")
        case "$CURRENT_DREAM" in
          *router.aod*) : ;; # resolved on its own - was just a transition gap
          *)
            # System Doze (or nothing) has genuinely taken over instead of
            # our dream. A plain wake alone was confirmed insufficient
            # here 2026-07-06 - this exact corrective action fired
            # repeatedly all evening (every few minutes) with Doze
            # immediately winning right back each time, meaning the fix
            # was never actually sticking. A force-stop + screensaver-
            # enabled toggle + wake is the established reliable sequence
            # (same one set_aod_layout.cgi uses) - a plain wake only
            # resumes/re-evaluates against whatever DreamManagerService
            # already has cached, while the toggle forces a genuinely
            # fresh dream-selection pass.
            am force-stop com.router.aod >/dev/null 2>&1
            settings put secure screensaver_enabled 0
            sleep 1
            settings put secure screensaver_enabled 1
            input keyevent KEYCODE_WAKEUP
            log "Doze/other dream ($CURRENT_DREAM) had taken over instead of ours - force-stopped + toggled screensaver + fresh wake to re-trigger selection"
            CHANGED=1
            ;;
        esac
        ;;
    esac
  fi

  [ "$CHANGED" = "0" ] || log "corrective action taken this pass"

  if [ "$((CHECK_COUNT % HEARTBEAT_EVERY))" = "0" ]; then
    log "heartbeat - watchdog alive, $CHECK_COUNT checks so far"
  fi

  sleep "$CHECK_INTERVAL"
done
