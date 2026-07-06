#!/system/bin/sh
# Logs the exact join time of any connected client MAC that isn't in
# device_names.conf - built 2026-07-06 after a mystery MAC
# (86:2d:6b:8b:0a:3e) showed up on the dashboard with no way to identify
# it after the fact. Checking the "locally administered" bit on all
# three connected MACs that day showed the TV and laptop use real fixed
# addresses (matching device_names.conf exactly), while the mystery MAC
# and both iPhones are all randomized - strongly suggesting it was one
# of the iPhones rotating its private WiFi address, not a genuine 5th
# device. This watcher makes future occurrences trivial to correlate:
# next time an unrecognized MAC joins, its exact timestamp is logged, so
# whatever you were doing at that moment (which phone's WiFi you toggled,
# etc.) tells you which device it actually was.
#
# Polls frequently (30s) specifically to catch the join moment precisely
# - the whole point is a tight timestamp, not just "it was unknown
# sometime today".

LOG=/data/adb/dashboard/new_device_events.log
STATE_FILE=/data/adb/dashboard/unknown_macs_seen
NAMES_FILE=/data/adb/dashboard/device_names.conf
AP_IF_CANDIDATES="ap_br_wlan2 wlan2"
CHECK_INTERVAL=30
BUSYBOX=/data/adb/magisk/busybox

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

detect_ap_if() {
  for IF in $AP_IF_CANDIDATES; do
    if ip addr show "$IF" >/dev/null 2>&1; then
      echo "$IF"
      return
    fi
  done
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

# Seed the state file with whatever's ALREADY connected at startup so
# those don't get misreported as "just joined" on the very first check -
# confirmed as a real false positive 2026-07-06 (the script's first-ever
# run logged the mystery MAC as a fresh join when its neighbor-table
# entry was actually STALE, i.e. old cached data, not a live reconnect).
# Only MACs that appear AFTER this baseline are genuinely new.
AP_IF_STARTUP=$(detect_ap_if)
if [ -n "$AP_IF_STARTUP" ]; then
  $BUSYBOX ip neigh show dev "$AP_IF_STARTUP" 2>/dev/null | grep lladdr | awk '{print tolower($3)}' | sort -u > "$STATE_FILE"
else
  : > "$STATE_FILE"
fi
while IFS= read -r BASELINE_MAC; do
  [ -z "$BASELINE_MAC" ] && continue
  grep -qi "^${BASELINE_MAC}=" "$NAMES_FILE" 2>/dev/null && continue
  log "Unrecognized MAC already connected at watcher startup (not a fresh join - exact join time unknown): $BASELINE_MAC"
done < "$STATE_FILE"

while true; do
  AP_IF=$(detect_ap_if)
  if [ -n "$AP_IF" ]; then
    CURRENT_MACS=$($BUSYBOX ip neigh show dev "$AP_IF" 2>/dev/null | grep lladdr | awk '{print tolower($3)}' | sort -u)

    for MAC in $CURRENT_MACS; do
      # Known device - skip.
      grep -qi "^${MAC}=" "$NAMES_FILE" 2>/dev/null && continue
      # Already logged this connection session - skip (avoid spamming
      # the same still-connected unknown MAC every 30s).
      grep -qi "^${MAC}$" "$STATE_FILE" 2>/dev/null && continue

      echo "$MAC" >> "$STATE_FILE"
      log "Unrecognized MAC joined: $MAC - not in device_names.conf. Check what you were doing right now (which phone's WiFi you toggled, any new device) to identify it, then add a line to $NAMES_FILE"
    done

    # Drop any MAC from the "already logged" state once it's no longer
    # connected, so a genuinely recurring unknown device gets a fresh
    # timestamp each time it reappears rather than only ever once.
    if [ -s "$STATE_FILE" ]; then
      NEW_STATE=$(mktemp)
      while IFS= read -r SEEN_MAC; do
        [ -z "$SEEN_MAC" ] && continue
        echo "$CURRENT_MACS" | grep -qi "^${SEEN_MAC}$" && echo "$SEEN_MAC" >> "$NEW_STATE"
      done < "$STATE_FILE"
      mv "$NEW_STATE" "$STATE_FILE"
    fi
  fi

  sleep "$CHECK_INTERVAL"
done
