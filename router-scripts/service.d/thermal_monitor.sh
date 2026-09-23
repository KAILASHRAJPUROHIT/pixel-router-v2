#!/system/bin/sh
# Thermal-aware throttle: when the device enters SEVERE thermal status
# (Android's own threshold, confirmed via dumpsys thermalservice), caps
# CPU frequency across all cores to cut app-layer heat generation.
# Deliberately does NOT touch anything network/modem-related - the
# cellular baseband runs on its own separate chip (Shannon), completely
# unaffected by AP-side CPU frequency changes. Restores full speed once
# thermal status drops back to normal.

LOG=/data/adb/dashboard/thermal_monitor.log
STATE_FILE=/data/adb/dashboard/thermal_throttled
CPU_COUNT=8
THROTTLE_STATUS_THRESHOLD=3   # SEVERE (0=none 1=light 2=moderate 3=severe 4=critical 5=emergency 6=shutdown)

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

get_thermal_status() {
  dumpsys thermalservice 2>/dev/null | grep -m1 "^Thermal Status:" | awk '{print $3}'
}

throttle_cpus() {
  # Drop to the lowest quarter of each core's available steps - this is
  # the emergency SEVERE+ tier, well below the permanent 70% baseline.
  for i in $(seq 0 $((CPU_COUNT - 1))); do
    F="/sys/devices/system/cpu/cpu$i/cpufreq"
    [ -d "$F" ] || continue
    AVAIL=$(cat "$F/scaling_available_frequencies" 2>/dev/null)
    [ -z "$AVAIL" ] && continue
    N=$(echo $AVAIL | wc -w)
    IDX=$(( (N + 3) / 4 ))
    TARGET=$(echo $AVAIL | tr ' ' '\n' | sort -n | sed -n "${IDX}p")
    [ -n "$TARGET" ] && echo "$TARGET" > "$F/scaling_max_freq" 2>/dev/null
  done
  GF=/sys/devices/platform/1c500000.mali
  if [ -d "$GF" ]; then
    GAVAIL=$(cat "$GF/available_frequencies" 2>/dev/null)
    if [ -n "$GAVAIL" ]; then
      GN=$(echo $GAVAIL | wc -w)
      GIDX=$(( (GN + 3) / 4 ))
      GTARGET=$(echo $GAVAIL | tr ' ' '\n' | sort -n | sed -n "${GIDX}p")
      [ -n "$GTARGET" ] && echo "$GTARGET" > "$GF/scaling_max_freq" 2>/dev/null
    fi
  fi
}

restore_cpus() {
  # Restore to the permanent 70%/60% baseline set at boot, NOT full
  # stock max - the baseline underclock stays in effect always.
  for i in $(seq 0 $((CPU_COUNT - 1))); do
    F="/sys/devices/system/cpu/cpu$i/cpufreq"
    [ -d "$F" ] || continue
    MAXF=$(cat "$F/cpuinfo_max_freq" 2>/dev/null)
    [ -z "$MAXF" ] && continue
    TARGET_RAW=$(( MAXF * 70 / 100 ))
    AVAIL=$(cat "$F/scaling_available_frequencies" 2>/dev/null)
    TARGET=$(echo $AVAIL | tr ' ' '\n' | sort -n | awk -v t="$TARGET_RAW" '$1<=t{best=$1} END{print best}')
    [ -z "$TARGET" ] && TARGET=$MAXF
    echo "$TARGET" > "$F/scaling_max_freq" 2>/dev/null
  done
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
}

while true; do
  STATUS=$(get_thermal_status)
  IS_THROTTLED=$(cat "$STATE_FILE" 2>/dev/null || echo 0)

  if [ -n "$STATUS" ] && [ "$STATUS" -ge "$THROTTLE_STATUS_THRESHOLD" ] 2>/dev/null; then
    if [ "$IS_THROTTLED" != "1" ]; then
      throttle_cpus
      echo 1 > "$STATE_FILE"
      log "Thermal status $STATUS (SEVERE+) - CPU frequency capped. Modem/WiFi untouched."
    fi
  else
    if [ "$IS_THROTTLED" = "1" ]; then
      restore_cpus
      echo 0 > "$STATE_FILE"
      log "Thermal status $STATUS (back to normal) - CPU frequency restored to full."
    fi
  fi

  sleep 30
done
