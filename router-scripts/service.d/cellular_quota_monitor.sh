#!/system/bin/sh
# Enforces a monthly cellular data cutoff (300GB, configurable billing
# cycle reset day) - added 2026-07-07. Deliberately does NOT duplicate
# any of failover_monitor.sh's borrowed-hotspot connection logic: once
# mobile data is disabled here, cellular throughput reads as 0/down to
# that script's own existing cellular-health check, which already knows
# how to borrow a family member's hotspot via SMS+WiFi. This script's
# only job is the on/off decision and the cycle-scoped byte counter.
STATE_DIR=/data/adb/dashboard
CYCLE_BYTES_FILE="$STATE_DIR/cellular_cycle_bytes"
CYCLE_DAY_FILE="$STATE_DIR/billing_cycle_day"
LAST_RESET_FILE="$STATE_DIR/billing_last_reset"
QUOTA_FLAG_FILE="$STATE_DIR/data_cutoff_active"
LOG="$STATE_DIR/cellular_quota_monitor.log"
CUTOFF_GB=300
CHECK_INTERVAL=300

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

# Same rmnet-only byte sum as update.sh's lifetime counter - cellular
# specifically, never wlan0/wlan2, so borrowed-hotspot browsing never
# counts against this quota.
sum_all_rmnet_bytes() {
  awk '
    $1 ~ /^rmnet[0-9]+:/ { rx+=$2; tx+=$10 }
    END { print rx+tx }' /proc/net/dev
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

[ -f "$CYCLE_DAY_FILE" ] || echo 1 > "$CYCLE_DAY_FILE"
[ -f "$CYCLE_BYTES_FILE" ] || echo 0 > "$CYCLE_BYTES_FILE"

PREV_SUM=$(sum_all_rmnet_bytes)

while true; do
  BILLING_DAY=$(cat "$CYCLE_DAY_FILE" 2>/dev/null)
  [ -z "$BILLING_DAY" ] && BILLING_DAY=1
  # Strip any leading zero from `date +%d` (busybox date has no reliable
  # "%-d" no-pad specifier) so numeric comparison against the plain
  # 1-28 value the dashboard dropdown stores works correctly.
  TODAY_DAY=$(date +%d | sed 's/^0//')
  TODAY_MONTH=$(date +%Y-%m)
  LAST_RESET=$(cat "$LAST_RESET_FILE" 2>/dev/null)

  # Reset exactly once per billing period - guards against re-resetting
  # on every 5-minute poll throughout the whole matching day.
  if [ "$TODAY_DAY" = "$BILLING_DAY" ] && [ "$LAST_RESET" != "$TODAY_MONTH" ]; then
    echo 0 > "$CYCLE_BYTES_FILE"
    echo "$TODAY_MONTH" > "$LAST_RESET_FILE"
    if [ -f "$QUOTA_FLAG_FILE" ]; then
      rm -f "$QUOTA_FLAG_FILE"
      svc data enable >/dev/null 2>&1
      log "New billing cycle started - cellular data re-enabled, cycle counter reset"
    else
      log "New billing cycle started - cycle counter reset"
    fi
  fi

  CUR_SUM=$(sum_all_rmnet_bytes)
  STORED=$(cat "$CYCLE_BYTES_FILE" 2>/dev/null || echo 0)
  # awk (double-precision), never shell $(( )) - same 32-bit overflow
  # class already fixed elsewhere in this codebase for cumulative byte
  # counters on this device.
  NEW_TOTAL=$(awk -v s="$STORED" -v c="$CUR_SUM" -v p="$PREV_SUM" 'BEGIN{
    delta = (c >= p) ? (c - p) : c
    printf "%.0f", s + delta
  }')
  echo "$NEW_TOTAL" > "$CYCLE_BYTES_FILE"
  PREV_SUM=$CUR_SUM

  CYCLE_GB=$(awk -v b="$NEW_TOTAL" 'BEGIN{printf "%.2f", b/1000000000}')

  if [ ! -f "$QUOTA_FLAG_FILE" ] && awk -v g="$CYCLE_GB" -v c="$CUTOFF_GB" 'BEGIN{exit !(g>=c)}'; then
    touch "$QUOTA_FLAG_FILE"
    svc data disable >/dev/null 2>&1
    log "Cellular usage this cycle (${CYCLE_GB}GB) reached the ${CUTOFF_GB}GB cutoff - mobile data disabled, staying on borrowed internet until day $BILLING_DAY next month"
  fi

  sleep "$CHECK_INTERVAL"
done
