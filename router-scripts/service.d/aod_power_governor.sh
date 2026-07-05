#!/system/bin/sh
# Two-tier CPU/GPU clocking on top of permanent_underclock.sh's 70%/60%
# baseline: drops further while the AOD is idly displaying (screen not
# actively used - Dozing/dreaming, whether showing our stats AOD, the
# Photos slideshow, or system Doze) since nothing latency-sensitive is
# happening then, and snaps back to the normal 70%/60% baseline the
# moment the user actually wakes/interacts with the phone, so real use
# never feels underpowered. Polls every 2s specifically so the snap-back
# on wake is fast enough not to be noticeable.
#
# Defers to thermal_monitor.sh's SEVERE+ emergency throttle - if that's
# already engaged, this script leaves clocks alone rather than fight it.

LOG=/data/adb/dashboard/aod_power_governor.log
THERMAL_STATE_FILE=/data/adb/dashboard/thermal_throttled
STATE_FILE=/data/adb/dashboard/aod_power_state
CPU_COUNT=8
POLL_INTERVAL=2

# Idle-AOD tier: well below the 70%/60% permanent baseline, but not so
# low it visibly stutters displaying stats or the Photos slideshow's
# image decode/transitions.
AOD_CPU_PCT=35
AOD_GPU_PCT=30
# Normal baseline - must stay in sync with permanent_underclock.sh.
BASE_CPU_PCT=70
BASE_GPU_PCT=60

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

set_cpu_pct() {
  PCT="$1"
  for i in $(seq 0 $((CPU_COUNT - 1))); do
    F="/sys/devices/system/cpu/cpu$i/cpufreq"
    [ -d "$F" ] || continue
    MAXF=$(cat "$F/cpuinfo_max_freq" 2>/dev/null)
    [ -z "$MAXF" ] && continue
    TARGET_RAW=$(( MAXF * PCT / 100 ))
    AVAIL=$(cat "$F/scaling_available_frequencies" 2>/dev/null)
    TARGET=$(echo $AVAIL | tr ' ' '\n' | sort -n | awk -v t="$TARGET_RAW" '$1<=t{best=$1} END{print best}')
    [ -z "$TARGET" ] && TARGET=$MAXF
    echo "$TARGET" > "$F/scaling_max_freq" 2>/dev/null
  done
}

set_gpu_pct() {
  PCT="$1"
  GF=/sys/devices/platform/1c500000.mali
  [ -d "$GF" ] || return
  GMAX=$(cat "$GF/max_freq" 2>/dev/null)
  [ -z "$GMAX" ] && return
  GTARGET_RAW=$(( GMAX * PCT / 100 ))
  GAVAIL=$(cat "$GF/available_frequencies" 2>/dev/null)
  GTARGET=$(echo $GAVAIL | tr ' ' '\n' | sort -n | awk -v t="$GTARGET_RAW" '$1<=t{best=$1} END{print best}')
  [ -z "$GTARGET" ] && GTARGET=$GMAX
  echo "$GTARGET" > "$GF/scaling_max_freq" 2>/dev/null
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 10

LAST_STATE="base"

while true; do
  # Thermal emergency throttle always wins - don't fight it.
  if [ "$(cat "$THERMAL_STATE_FILE" 2>/dev/null || echo 0)" = "1" ]; then
    sleep "$POLL_INTERVAL"
    continue
  fi

  WAKEFULNESS=$(dumpsys power 2>/dev/null | grep -m1 mWakefulness= | sed 's/.*mWakefulness=//')

  if [ "$WAKEFULNESS" = "Awake" ]; then
    WANT_STATE="base"
  else
    WANT_STATE="aod"
  fi

  if [ "$WANT_STATE" != "$LAST_STATE" ]; then
    if [ "$WANT_STATE" = "aod" ]; then
      set_cpu_pct "$AOD_CPU_PCT"
      set_gpu_pct "$AOD_GPU_PCT"
      log "Idle (wakefulness=$WAKEFULNESS) - dropped to AOD-tier clocks (${AOD_CPU_PCT}%/${AOD_GPU_PCT}%)"
    else
      set_cpu_pct "$BASE_CPU_PCT"
      set_gpu_pct "$BASE_GPU_PCT"
      log "Awake - restored to baseline clocks (${BASE_CPU_PCT}%/${BASE_GPU_PCT}%)"
    fi
    LAST_STATE="$WANT_STATE"
    echo "$WANT_STATE" > "$STATE_FILE"
  fi

  sleep "$POLL_INTERVAL"
done
