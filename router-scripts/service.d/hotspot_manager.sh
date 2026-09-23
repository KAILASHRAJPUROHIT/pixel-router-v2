#!/system/bin/sh
# Coordinating supervisor - added 2026-07-08. Every other watchdog on
# this device watches one narrow thing (is the hotspot interface up? is
# httpd running? is the tunnel alive?) and fixes only that. Real
# failures are often visible only by combining signals none of those
# narrow watchdogs individually see:
#
#   - A script restarting over and over (service_supervisor.sh has no
#     upper bound - it'll retry forever, silently) means "keep
#     restarting it" isn't actually working. Nothing currently tells
#     the owner that.
#   - internet_type=borrowed with the hotspot AP up and clients
#     connected, but no real internet - means the BORROWED phone's own
#     data died, not anything on this device. hotspot_watchdog.sh only
#     checks "does the AP interface exist", so this looks perfectly
#     healthy to it.
#   - Dashboard, hotspot, and AOD all down at once is a different class
#     of problem than any one of them being down alone - each of their
#     own watchdogs will keep retrying their own narrow fix forever
#     without ever considering that the underlying Wi-Fi/system stack
#     itself needs a full bounce.
#   - Sustained poor signal while still on-cellular is a leading
#     indicator, not yet a failure - nothing currently gives the owner
#     a heads-up before failover_monitor.sh's own harder trigger fires.
#
# This is deliberately advisory/escalation-only for anything
# failover_monitor.sh or vpn_tether_routing.sh already owns the state
# machine for - it observes and alerts (SMS, reusing the same
# explicit-component broadcast every other script here already uses)
# rather than directly manipulating their state, to avoid two scripts
# racing to control the same thing.
STATE_DIR=/data/adb/dashboard
LOG="$STATE_DIR/hotspot_manager.log"
STATUS_FILE="$STATE_DIR/hotspot_manager_status"
OPS_COUNTER_FILE="$STATE_DIR/hotspot_manager_ops_today"
SUPERVISOR_LOG="$STATE_DIR/service_supervisor.log"
FAILOVER_CONFIG="$STATE_DIR/failover.properties"
FLAP_THRESHOLD=4
POOR_SIGNAL_STREAK_BEFORE_ALERT=10   # ~10 min at MIN_INTERVAL/check
FULL_DOWN_STREAK_BEFORE_REBOOT=3     # ~3 checks, not a fixed time - the interval itself shrinks when unhealthy
ALERT_COOLDOWN_SECONDS=1800          # don't re-alert the same condition more than once per 30 min

# Heat/network budget - added 2026-07-08. Every check here costs real
# CPU wake-ups (heat) and at least one network round-trip (ping, plus a
# dumpsys telephony call that's the heaviest single thing this script
# does). Polling at a fixed 60s forever spends that cost identically
# whether anything is wrong or not - this device is already watched by
# a dozen other watchdogs at similar cadences, so a coordinating
# supervisor doesn't need to poll as tight as the narrow ones it's
# layered on top of. Backs off toward MAX_INTERVAL after sustained
# healthy checks, snaps straight back to MIN_INTERVAL the instant
# anything looks off - the budget is spent on responsiveness only when
# there's something to respond to, not on confirming "still fine" over
# and over.
MIN_INTERVAL=60
MAX_INTERVAL=300
BACKOFF_AFTER_HEALTHY_STREAK=5   # start stretching the interval after this many clean checks in a row

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

send_sms() {
  NUMBER="$1"
  MESSAGE="$2"
  [ -z "$NUMBER" ] && return
  am broadcast -n com.router.aod/.SendSmsReceiver -a com.router.aod.SEND_SMS --es number "$NUMBER" --es message "$MESSAGE" >/dev/null 2>&1
}

owner_number() {
  grep "^phone1.number=" "$FAILOVER_CONFIG" 2>/dev/null | cut -d= -f2-
}

# Cooldown-gated alert - $1 = a short condition key (used as a state
# filename), $2 = message. Only actually sends once per
# ALERT_COOLDOWN_SECONDS for the same condition, so a persistent problem
# doesn't spam SMS every single check.
alert_once() {
  KEY="$1"
  MESSAGE="$2"
  COOLDOWN_FILE="$STATE_DIR/manager_alert_${KEY}"
  LAST=$(cat "$COOLDOWN_FILE" 2>/dev/null || echo 0)
  NOW=$(date +%s)
  if [ "$((NOW - LAST))" -ge "$ALERT_COOLDOWN_SECONDS" ]; then
    log "ALERT [$KEY]: $MESSAGE"
    OWNER=$(owner_number)
    [ -n "$OWNER" ] && send_sms "$OWNER" "Router: $MESSAGE"
    echo "$NOW" > "$COOLDOWN_FILE"
  fi
}

hotspot_up() {
  [ -d /sys/class/net/wlan2 ] || [ -d /sys/class/net/ap_br_wlan2 ]
}

dashboard_up() {
  ps -ef | grep -F "busybox httpd" | grep -F "8080" | grep -v grep >/dev/null 2>&1
}

aod_alive() {
  # Freshness, not just "is the process running" - matches the same
  # signal used to diagnose the 2026-07-08 "AOD looks off" report
  # (fresh preview capture proved it was actually rendering fine).
  PREVIEW=/data/data/com.router.aod/files/aod_preview.png
  [ -f "$PREVIEW" ] || return 1
  MTIME=$(stat -c %Y "$PREVIEW" 2>/dev/null)
  [ -z "$MTIME" ] && return 1
  NOW=$(date +%s)
  [ "$((NOW - MTIME))" -lt 60 ]
}

real_internet() {
  count_op
  ping -c 1 -W 2 8.8.8.8 >/dev/null 2>&1
}

# Lightweight daily counter of heavy operations (ping/dumpsys calls) -
# purely observability, not an enforced cap (a hard cap would blunt
# responsiveness in exactly the scenario - a real ongoing problem -
# where checking often actually matters most). Resets automatically
# each day since the file is keyed by date.
count_op() {
  TODAY=$(date '+%Y-%m-%d')
  RECORDED_DATE=$(cat "$OPS_COUNTER_FILE.date" 2>/dev/null)
  if [ "$RECORDED_DATE" != "$TODAY" ]; then
    echo "$TODAY" > "$OPS_COUNTER_FILE.date"
    echo 0 > "$OPS_COUNTER_FILE"
  fi
  N=$(cat "$OPS_COUNTER_FILE" 2>/dev/null || echo 0)
  echo "$((N + 1))" > "$OPS_COUNTER_FILE"
}

check_flapping() {
  [ -f "$SUPERVISOR_LOG" ] || return
  # busybox date has no reliable relative-time parsing, so this counts
  # restarts in the last N log lines rather than a true time window -
  # service_supervisor.sh checks every 120s and logs one line per
  # restart, so 300 lines covers a good while and still catches
  # sustained flapping without needing real timestamp math.
  for SCRIPT in hotspot_watchdog.sh dashboard_watchdog.sh cloudflared_tunnel.sh vpn_tether_routing.sh failover_monitor.sh; do
    COUNT=$(tail -300 "$SUPERVISOR_LOG" 2>/dev/null | grep -c "$SCRIPT was not running - restarted")
    if [ "$COUNT" -ge "$FLAP_THRESHOLD" ]; then
      alert_once "flap_${SCRIPT}" "$SCRIPT has restarted $COUNT times recently - it keeps dying and restarting isn't fixing it, worth checking manually."
    fi
  done
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 90

FULL_DOWN_STREAK=0
POOR_SIGNAL_STREAK=0
HEALTHY_STREAK=0
CURRENT_INTERVAL=$MIN_INTERVAL

while true; do
  INTERNET_TYPE=$(cat "$STATE_DIR/internet_type" 2>/dev/null)
  HOTSPOT_OK=0; hotspot_up && HOTSPOT_OK=1
  DASH_OK=0; dashboard_up && DASH_OK=1
  AOD_OK=0; aod_alive && AOD_OK=1
  NET_OK=0; real_internet && NET_OK=1

  # --- Borrowed source has no real internet ---
  if [ "$INTERNET_TYPE" = "borrowed" ] && [ "$HOTSPOT_OK" = "1" ] && [ "$NET_OK" = "0" ]; then
    alert_once "borrowed_no_internet" "on borrowed WiFi and the hotspot itself is up, but there's no real internet - the source phone's own data may be off or out of range."
  fi

  # --- Sustained poor signal on own cellular (leading indicator) ---
  # Skipped entirely on borrowed WiFi - dumpsys telephony.registry is
  # the single heaviest call this script makes, and RSRP is meaningless
  # when cellular isn't even the active uplink right now.
  RSRP=""
  if [ "$INTERNET_TYPE" = "self" ]; then
    count_op
    RSRP=$(dumpsys telephony.registry 2>/dev/null | grep -m1 "mSignalStrength=" | grep -oE "mLte=CellSignalStrengthLte:[^,]*rsrp=-?[0-9]+" | grep -oE 'rsrp=-?[0-9]+' | cut -d= -f2)
  fi
  if [ "$INTERNET_TYPE" = "self" ] && [ -n "$RSRP" ] && [ "$RSRP" -lt -100 ] 2>/dev/null; then
    POOR_SIGNAL_STREAK=$((POOR_SIGNAL_STREAK + 1))
  else
    POOR_SIGNAL_STREAK=0
  fi
  if [ "$POOR_SIGNAL_STREAK" -ge "$POOR_SIGNAL_STREAK_BEFORE_ALERT" ]; then
    alert_once "poor_signal" "cellular signal has been poor for a while (rsrp=${RSRP}) - may be worth switching to borrowed WiFi before it drops entirely."
    POOR_SIGNAL_STREAK=0
  fi

  # --- Full-system-down escalation ---
  if [ "$HOTSPOT_OK" = "0" ] && [ "$DASH_OK" = "0" ] && [ "$AOD_OK" = "0" ]; then
    FULL_DOWN_STREAK=$((FULL_DOWN_STREAK + 1))
    log "full-system-down check $FULL_DOWN_STREAK/$FULL_DOWN_STREAK_BEFORE_REBOOT (hotspot/dashboard/AOD all unhealthy)"
  else
    FULL_DOWN_STREAK=0
  fi
  if [ "$FULL_DOWN_STREAK" -ge "$FULL_DOWN_STREAK_BEFORE_REBOOT" ]; then
    log "hotspot, dashboard, and AOD all down for $FULL_DOWN_STREAK consecutive checks - individual watchdogs aren't recovering this, rebooting as last resort"
    OWNER=$(owner_number)
    [ -n "$OWNER" ] && send_sms "$OWNER" "Router: hotspot/dashboard/AOD all down and not recovering - rebooting now."
    FULL_DOWN_STREAK=0
    ( sleep 3; su -c reboot ) &
  fi

  check_flapping

  # --- Adaptive interval: the actual heat/network budget ---
  # A fully healthy pass (everything OK, no growing poor-signal streak)
  # counts toward backing off; anything less snaps straight back to
  # MIN_INTERVAL so a real problem always gets the tight cadence, never
  # the stretched-out one.
  if [ "$HOTSPOT_OK" = "1" ] && [ "$DASH_OK" = "1" ] && [ "$AOD_OK" = "1" ] && [ "$NET_OK" = "1" ] && [ "$POOR_SIGNAL_STREAK" = "0" ]; then
    HEALTHY_STREAK=$((HEALTHY_STREAK + 1))
  else
    HEALTHY_STREAK=0
  fi

  if [ "$HEALTHY_STREAK" -ge "$BACKOFF_AFTER_HEALTHY_STREAK" ]; then
    CURRENT_INTERVAL=$((CURRENT_INTERVAL * 2))
    [ "$CURRENT_INTERVAL" -gt "$MAX_INTERVAL" ] && CURRENT_INTERVAL=$MAX_INTERVAL
  else
    CURRENT_INTERVAL=$MIN_INTERVAL
  fi

  cat > "$STATUS_FILE.tmp" <<EOF
{"hotspot":$HOTSPOT_OK,"dashboard":$DASH_OK,"aod":$AOD_OK,"internet":$NET_OK,"internet_type":"$INTERNET_TYPE","poll_interval_s":$CURRENT_INTERVAL,"ops_today":$(cat "$OPS_COUNTER_FILE" 2>/dev/null || echo 0),"ts":$(date +%s)}
EOF
  mv "$STATUS_FILE.tmp" "$STATUS_FILE"

  sleep "$CURRENT_INTERVAL"
done
