#!/system/bin/sh
# Runs once at boot. Permanently reduces baseline heat generation for a
# stationary router role, where full CPU/GPU burst speed is never
# actually needed (NAT/hotspot/DNS/our scripts are all light work).
#
# Does NOT touch anything network/modem-related - CPU and GPU are
# separate silicon from the cellular baseband (Shannon chip), so this
# has zero effect on hotspot/network throughput.
#
# This sets the BASELINE cap. The reactive thermal_monitor.sh throttles
# further from this baseline only during SEVERE+ thermal events, and
# restores back to this baseline (not full stock max) once clear.

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 5

# --- Lock refresh rate to 60Hz - saves compositor/GPU wakeups with
#     zero downside since nothing is ever watching this screen. ---
settings put system peak_refresh_rate 60
settings put system min_refresh_rate 60

# --- Disable the system's own Ambient Display (Doze) so our custom
#     AOD Dream (pixel-router-aod) can actually take over during idle
#     instead of being pre-empted by stock ambient display - found
#     2026-07-04 that Doze otherwise wins priority over a user Dream
#     whenever the hardware ambient display is available and enabled.
#     (aod_watchdog.sh re-asserts this + related settings periodically,
#     since they've been observed to silently revert on their own.) ---
settings put secure doze_enabled 0

# --- Disable heads-up notification banners so an incoming notification
#     never wakes/interrupts the AOD display - found 2026-07-05. ---
settings put global heads_up_notifications_enabled 0

# --- CPU: cap each core to ~70% of its own max as a permanent baseline. ---
for i in 0 1 2 3 4 5 6 7; do
  F="/sys/devices/system/cpu/cpu$i/cpufreq"
  [ -d "$F" ] || continue
  MAXF=$(cat "$F/cpuinfo_max_freq" 2>/dev/null)
  [ -z "$MAXF" ] && continue
  TARGET_RAW=$(( MAXF * 70 / 100 ))
  # Snap to the nearest available step at or below the target.
  AVAIL=$(cat "$F/scaling_available_frequencies" 2>/dev/null)
  TARGET=$(echo $AVAIL | tr ' ' '\n' | sort -n | awk -v t="$TARGET_RAW" '$1<=t{best=$1} END{print best}')
  [ -z "$TARGET" ] && TARGET=$MAXF
  echo "$TARGET" > "$F/scaling_max_freq" 2>/dev/null
done

# --- GPU: cap similarly. ---
GF=/sys/devices/platform/1c500000.mali
if [ -d "$GF" ]; then
  GMAX=$(cat "$GF/max_freq" 2>/dev/null)
  if [ -n "$GMAX" ]; then
    GTARGET_RAW=$(( GMAX * 60 / 100 ))
    GAVAIL=$(cat "$GF/available_frequencies" 2>/dev/null)
    GTARGET=$(echo $GAVAIL | tr ' ' '\n' | sort -n | awk -v t="$GTARGET_RAW" '$1<=t{best=$1} END{print best}')
    [ -z "$GTARGET" ] && GTARGET=$GMAX
    echo "$GTARGET" > "$GF/scaling_max_freq" 2>/dev/null
  fi
fi

echo "$(date '+%Y-%m-%d %H:%M:%S') - permanent underclock + 60Hz lock + doze disabled + heads-up disabled applied" >> /data/adb/dashboard/underclock.log
