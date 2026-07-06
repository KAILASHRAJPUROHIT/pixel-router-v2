#!/system/bin/sh
# Detects the router's own cellular connection going bad (quota
# exhausted, carrier outage, etc.) and fails over to a family member's
# iPhone Personal Hotspot so devices on our own hotspot (PILLYBOO) keep
# working, instead of everyone silently losing internet.
#
# How the handoff actually works:
# - This phone's hardware confirmed to support simultaneous AP+STA
#   (`cmd wifi get-softap-supported-features` -> wifi_softap_bridged_ap_
#   with_sta_supported, dumpsys wifi -> "STA + AP Concurrency Supported:
#   true") - so our own hotspot keeps running the whole time; we're just
#   adding a second, upstream Wi-Fi connection alongside it.
# - iPhones give no external API to remotely flip Personal Hotspot on,
#   and they can't reliably self-detect "am I home" either (their WiFi
#   is normally off at home specifically to avoid loading the router,
#   so LAN presence isn't a usable signal, and doing distance math
#   inside a Shortcuts "If" block proved fragile to set up). So this is
#   a two-phase SMS handshake instead of a single trigger:
#     1. WHEREAREYOU_PHRASE goes out - each iPhone has a trivial
#        Shortcut that replies with "LOC:<lat>,<lon>" (no conditionals
#        on the iPhone side at all).
#     2. LocationReplyReceiver (in the AOD app) catches that reply and
#        writes it to this app's private storage; this script computes
#        the actual distance from home itself (haversine, see below)
#        and only then sends SMS_TRIGGER_PHRASE - which each iPhone's
#        second, equally trivial Shortcut turns straight into
#        "Set Personal Hotspot On", unconditionally, since the router
#        already did the location filtering.
# - Once an iPhone's hotspot is up, we join it as a normal Wi-Fi client
#   (`cmd wifi connect-network`). Android's own tethering stack
#   automatically re-homes the hotspot's upstream to whatever the
#   device considers its current best network - this is standard native
#   behavior for a real Wi-Fi network (unlike vpn_tether_routing.sh's
#   problem, which is specifically that Android tethering refuses to
#   follow app-layer VPN tunnels - a real STA connection isn't a VPN and
#   isn't excluded). No custom iptables/NAT rules needed for this part.
# - Every 5 min while borrowed, re-check our own cellular. Once it's
#   genuinely back, disconnect from the borrowed network and let
#   tethering upstream naturally fall back to cellular.

LOG=/data/adb/dashboard/failover_monitor.log
STATE_FILE=/data/adb/dashboard/internet_type   # "self" or "borrowed"
CONFIG=/data/adb/dashboard/failover.properties

CHECK_INTERVAL=120          # 2 min between checks while on our own connection
LOW_SPEED_THRESHOLD_MBPS=1
CONSECUTIVE_BAD_NEEDED=3    # ~6 min of sustained bad readings before failing over
RECOVERY_CHECK_INTERVAL=300 # 5 min between checks while borrowed
HOTSPOT_SCAN_TIMEOUT=180    # give the iPhones up to 3 min to actually enable+appear
SMS_TRIGGER_PHRASE="FAILOVER_TRIGGER_PHRASE_DO_NOT_CHANGE"

# Two-phase handshake: ask where each phone is BEFORE asking it to turn
# on Personal Hotspot - the phones can't reliably self-detect "am I
# home" on their own (their WiFi is normally off at home to reduce load
# on the router, so LAN presence isn't a usable signal, and doing the
# distance math inside a Shortcuts "If" block is fragile/fiddly to set
# up) - so the router does the distance calculation itself instead.
WHEREAREYOU_PHRASE="WHEREAREYOU_TRIGGER_PHRASE_DO_NOT_CHANGE"
LOCATION_REPLY_TIMEOUT=60   # seconds to wait for a phone's GPS-reply text
HOME_RADIUS_KM=0.2          # ~200m - matches the buffer discussed for GPS drift
APP_FILES_DIR=/data/data/com.router.aod/files

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

set_internet_type() {
  echo "$1" > "$STATE_FILE"
}

# Config file (gitignored, same pattern as dashboard.properties):
#   phone1.number=+91XXXXXXXXXX
#   phone1.ssid=iPhone (Kailash)
#   phone1.password=xxxxxxxx
#   phone2.number=+91XXXXXXXXXX
#   phone2.ssid=iPhone (Wife)
#   phone2.password=xxxxxxxx
#   home.lat=19.8036
#   home.lon=72.7560
#   phone1.mac=xx:xx:xx:xx:xx:xx   (as seen connected to PILLYBOO - for the WiFi presence tier)
#   phone2.mac=xx:xx:xx:xx:xx:xx
load_config() {
  PHONE1_NUMBER=$(grep "^phone1.number=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_SSID=$(grep "^phone1.ssid=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_PASSWORD=$(grep "^phone1.password=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_NUMBER=$(grep "^phone2.number=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_SSID=$(grep "^phone2.ssid=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_PASSWORD=$(grep "^phone2.password=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  HOME_LAT=$(grep "^home.lat=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  HOME_LON=$(grep "^home.lon=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_MAC=$(grep "^phone1.mac=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_MAC=$(grep "^phone2.mac=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
}

detect_ap_if() {
  if ip addr show ap_br_wlan2 >/dev/null 2>&1; then
    echo ap_br_wlan2
  else
    echo wlan2
  fi
}

# Cheapest/fastest presence tier: is this MAC currently a connected
# client on our OWN hotspot right now? Only catches phones that happen
# to have WiFi on - both phones normally keep it off at home to avoid
# loading the router, so this is a "best case, skip the SMS round-trip
# if we get lucky" check, not the primary mechanism.
wifi_presence_check() {
  MAC="$1"
  [ -z "$MAC" ] && return 1
  AP_IF=$(detect_ap_if)
  ip neigh show dev "$AP_IF" 2>/dev/null | grep -qi "$MAC"
}

detect_wan_if() {
  CANDIDATE=$(ip route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if ($i=="dev") print $(i+1)}' | grep '^rmnet' | head -1)
  if [ -n "$CANDIDATE" ]; then
    echo "$CANDIDATE"
  else
    awk '$1 ~ /^rmnet[0-9]+:/ { print $1, $2+$10 }' /proc/net/dev | sort -k2 -rn | head -1 | sed 's/://' | awk '{print $1}'
  fi
}

get_bytes() {
  awk -v ifc="$1:" -v field="$2" '
    $1==ifc { if (field=="rx") print $2; else print $10; found=1 }
    END { if (!found) print 0 }' /proc/net/dev
}

measure_cellular_mbps() {
  WAN_IF=$(detect_wan_if)
  [ -z "$WAN_IF" ] && { echo 0; return; }
  R1=$(get_bytes "$WAN_IF" rx)
  sleep 5
  R2=$(get_bytes "$WAN_IF" rx)
  awk -v r2="$R2" -v r1="$R1" 'BEGIN{d=(r2-r1)/5; if(d<0)d=0; printf "%.2f", d*8/1000000}'
}

cellular_reachable() {
  ping -c 1 -W 2 8.8.8.8 >/dev/null 2>&1
}

# True if WiFi is enabled AND actively connected to EITHER known phone's
# hotspot SSID - not just the specific one last recorded in
# borrowed_ssid. Both iPhone hotspot networks are saved simultaneously on
# this device (confirmed via `cmd wifi list-networks` 2026-07-06), so
# Android's own network selection can silently roam from one to the
# other on its own, with no call through this script at all - that left
# borrowed_ssid stale (recorded "BEENAS IPHONE 16 PRO MAX" while actually
# connected to "Kailash's 16 Pro Max"). Echoes the live SSID back so the
# caller can self-heal that file instead of just detecting the mismatch.
#
# The main loop's "borrowed" branch used to only ever check whether OUR
# OWN cellular had recovered, never whether the borrowed WiFi connection
# itself was still up at all - if WiFi got turned off entirely (crash,
# OS, a manual toggle), the loop kept silently logging "staying on
# borrowed hotspot" every 5 minutes with no actual connection to
# anywhere, and the dashboard kept showing a false "Borrowed" label.
# Confirmed as a real incident 2026-07-06.
borrowed_connection_alive() {
  [ "$(settings get global wifi_on 2>/dev/null)" = "1" ] || return 1
  CONNECTED_NOW=$(cmd wifi status 2>/dev/null | grep -o 'Wifi is connected to "[^"]*"' | sed 's/Wifi is connected to "//; s/"$//')
  [ -z "$CONNECTED_NOW" ] && return 1
  if [ "$CONNECTED_NOW" = "$PHONE1_SSID" ] || [ "$CONNECTED_NOW" = "$PHONE2_SSID" ]; then
    echo "$CONNECTED_NOW"
    return 0
  fi
  return 1
}

send_sms() {
  NUMBER="$1"
  MESSAGE="$2"
  [ -z "$NUMBER" ] && return
  # Must target the receiver explicitly (-n) - confirmed 2026-07-06 that
  # an implicit action-only broadcast never reaches it at all (Android
  # 8+ blocks implicit broadcasts to manifest-declared receivers, even
  # exported ones, specifically to stop apps waking on arbitrary custom
  # actions; explicit-component broadcasts aren't subject to that).
  am broadcast -n com.router.aod/.SendSmsReceiver -a com.router.aod.SEND_SMS --es number "$NUMBER" --es message "$MESSAGE" >/dev/null 2>&1
}

# Haversine great-circle distance in km between two lat/lon pairs.
# POSIX awk has sin/cos/atan2/sqrt built in - no extra tools needed.
haversine_km() {
  awk -v lat1="$1" -v lon1="$2" -v lat2="$3" -v lon2="$4" 'BEGIN {
    pi = atan2(0, -1)
    rad = pi / 180
    R = 6371
    dlat = (lat2 - lat1) * rad
    dlon = (lon2 - lon1) * rad
    a = sin(dlat/2)^2 + cos(lat1*rad) * cos(lat2*rad) * sin(dlon/2)^2
    c = 2 * atan2(sqrt(a), sqrt(1-a))
    printf "%.3f", R * c
  }'
}

# Reads whatever LocationReplyReceiver last wrote for this number (it
# only writes lat,lon,epoch_timestamp when a reply actually parses as
# LOC:<lat>,<lon>). Prints "lat,lon" if a reply arrived within the last
# LOCATION_REPLY_TIMEOUT seconds, otherwise prints nothing.
read_location_reply() {
  NUMBER="$1"
  SANITIZED=$(echo "$NUMBER" | tr -d -c '0-9')
  FILE="$APP_FILES_DIR/location_$SANITIZED.txt"
  [ -f "$FILE" ] || return
  LINE=$(cat "$FILE" 2>/dev/null)
  REPLY_TS=$(echo "$LINE" | cut -d, -f3)
  NOW=$(date +%s)
  [ -z "$REPLY_TS" ] && return
  AGE=$((NOW - REPLY_TS))
  [ "$AGE" -lt 0 ] && return
  if [ "$AGE" -le "$LOCATION_REPLY_TIMEOUT" ]; then
    echo "$LINE" | cut -d, -f1,2
  fi
}

# Queries both phones' locations, waits for replies, and returns
# (via stdout, one per line) the number of each phone confirmed to be
# within HOME_RADIUS_KM of home. Tries the cheap WiFi-presence tier
# first (skips the SMS round-trip entirely if a phone happens to have
# WiFi on); only falls through to the SMS/GPS handshake for whichever
# phone(s) the WiFi check didn't confirm.
find_phones_at_home() {
  FOUND1=0
  FOUND2=0

  if wifi_presence_check "$PHONE1_MAC"; then
    log "Phone1 confirmed home via WiFi presence (connected to PILLYBOO) - skipping SMS query"
    echo "1"
    FOUND1=1
  fi
  if wifi_presence_check "$PHONE2_MAC"; then
    log "Phone2 confirmed home via WiFi presence (connected to PILLYBOO) - skipping SMS query"
    echo "2"
    FOUND2=1
  fi

  # Only phones not already confirmed via WiFi need the SMS/GPS handshake.
  NEED_SMS1=0; [ "$FOUND1" = "0" ] && [ -n "$PHONE1_NUMBER" ] && NEED_SMS1=1
  NEED_SMS2=0; [ "$FOUND2" = "0" ] && [ -n "$PHONE2_NUMBER" ] && NEED_SMS2=1
  if [ "$NEED_SMS1" = "0" ] && [ "$NEED_SMS2" = "0" ]; then
    return
  fi

  # Clear out any stale reply files from a previous round so an old
  # location can't be mistaken for a fresh one.
  rm -f "$APP_FILES_DIR"/location_*.txt 2>/dev/null

  [ "$NEED_SMS1" = "1" ] && { send_sms "$PHONE1_NUMBER" "$WHEREAREYOU_PHRASE"; log "Sent WHEREAREYOU to $PHONE1_NUMBER"; }
  [ "$NEED_SMS2" = "1" ] && { send_sms "$PHONE2_NUMBER" "$WHEREAREYOU_PHRASE"; log "Sent WHEREAREYOU to $PHONE2_NUMBER"; }

  # Phones already confirmed via WiFi count as "found" for the loop's
  # exit condition below - only the ones needing SMS should keep it
  # spinning.
  FOUND1=$([ "$NEED_SMS1" = "1" ] && echo 0 || echo 1)
  FOUND2=$([ "$NEED_SMS2" = "1" ] && echo 0 || echo 1)

  ELAPSED=0
  while [ "$ELAPSED" -lt "$LOCATION_REPLY_TIMEOUT" ]; do
    sleep 5
    ELAPSED=$((ELAPSED + 5))

    if [ "$FOUND1" = "0" ] && [ -n "$PHONE1_NUMBER" ]; then
      REPLY=$(read_location_reply "$PHONE1_NUMBER")
      if [ -n "$REPLY" ]; then
        LAT=$(echo "$REPLY" | cut -d, -f1); LON=$(echo "$REPLY" | cut -d, -f2)
        DIST=$(haversine_km "$HOME_LAT" "$HOME_LON" "$LAT" "$LON")
        log "Phone1 replied: ${DIST}km from home"
        if awk -v d="$DIST" -v r="$HOME_RADIUS_KM" 'BEGIN{exit !(d<=r)}'; then
          echo "1"
        fi
        FOUND1=1
      fi
    fi
    if [ "$FOUND2" = "0" ] && [ -n "$PHONE2_NUMBER" ]; then
      REPLY=$(read_location_reply "$PHONE2_NUMBER")
      if [ -n "$REPLY" ]; then
        LAT=$(echo "$REPLY" | cut -d, -f1); LON=$(echo "$REPLY" | cut -d, -f2)
        DIST=$(haversine_km "$HOME_LAT" "$HOME_LON" "$LAT" "$LON")
        log "Phone2 replied: ${DIST}km from home"
        if awk -v d="$DIST" -v r="$HOME_RADIUS_KM" 'BEGIN{exit !(d<=r)}'; then
          echo "2"
        fi
        FOUND2=1
      fi
    fi

    PHONE1_DONE=0; [ "$FOUND1" = "1" ] || [ -z "$PHONE1_NUMBER" ] && PHONE1_DONE=1
    PHONE2_DONE=0; [ "$FOUND2" = "1" ] || [ -z "$PHONE2_NUMBER" ] && PHONE2_DONE=1
    [ "$PHONE1_DONE" = "1" ] && [ "$PHONE2_DONE" = "1" ] && break
  done

  [ "$FOUND1" = "0" ] && [ -n "$PHONE1_NUMBER" ] && log "Phone1 never replied within ${LOCATION_REPLY_TIMEOUT}s - treating as not home"
  [ "$FOUND2" = "0" ] && [ -n "$PHONE2_NUMBER" ] && log "Phone2 never replied within ${LOCATION_REPLY_TIMEOUT}s - treating as not home"
}

# There's no plain `cmd wifi disconnect` on this build - forget-network
# needs the numeric <networkId> from list-networks (SSID may contain
# spaces, but network id is always column 1, so awk '{print $1}' is
# safe regardless).
forget_network_by_ssid() {
  SSID="$1"
  [ -z "$SSID" ] && return
  NETID=$(cmd wifi list-networks 2>/dev/null | grep -F "$SSID" | head -1 | awk '{print $1}')
  if [ -n "$NETID" ] && [ "$NETID" != "Network" ]; then
    cmd wifi forget-network "$NETID" >/dev/null 2>&1
    log "Forgot network $SSID (id $NETID)"
  fi
}

# Scans for either APPROVED iPhone SSID (i.e. one confirmed at home by
# find_phones_at_home - $1/$2 are "1" if phone1/phone2 is approved),
# connects to whichever is seen first. Retries within
# HOTSPOT_SCAN_TIMEOUT since the iPhone needs a few seconds to actually
# enable its hotspot after the SMS.
try_connect_to_any_hotspot() {
  APPROVE1="$1"
  APPROVE2="$2"
  ELAPSED=0
  while [ "$ELAPSED" -lt "$HOTSPOT_SCAN_TIMEOUT" ]; do
    cmd wifi start-scan >/dev/null 2>&1
    sleep 8
    SCAN_RESULTS=$(cmd wifi list-scan-results 2>/dev/null)

    if [ "$APPROVE1" = "1" ] && [ -n "$PHONE1_SSID" ] && echo "$SCAN_RESULTS" | grep -qF "$PHONE1_SSID"; then
      log "Found $PHONE1_SSID - connecting"
      cmd wifi connect-network "$PHONE1_SSID" wpa2 "$PHONE1_PASSWORD" >/dev/null 2>&1
      echo "$PHONE1_SSID"
      return 0
    fi
    if [ "$APPROVE2" = "1" ] && [ -n "$PHONE2_SSID" ] && echo "$SCAN_RESULTS" | grep -qF "$PHONE2_SSID"; then
      log "Found $PHONE2_SSID - connecting"
      cmd wifi connect-network "$PHONE2_SSID" wpa2 "$PHONE2_PASSWORD" >/dev/null 2>&1
      echo "$PHONE2_SSID"
      return 0
    fi

    ELAPSED=$((ELAPSED + 10))
  done
  return 1
}

trigger_failover() {
  log "Cellular down/slow for $CONSECUTIVE_BAD_NEEDED consecutive checks - triggering failover"

  AT_HOME=$(find_phones_at_home)
  APPROVE1=0; APPROVE2=0
  echo "$AT_HOME" | grep -q "^1$" && APPROVE1=1
  echo "$AT_HOME" | grep -q "^2$" && APPROVE2=1

  if [ "$APPROVE1" = "0" ] && [ "$APPROVE2" = "0" ]; then
    log "Neither phone confirmed at home - no hotspot request sent this cycle, will retry next poll"
    return 1
  fi

  [ "$APPROVE1" = "1" ] && { send_sms "$PHONE1_NUMBER" "$SMS_TRIGGER_PHRASE"; log "Phone1 confirmed home - sent hotspot-enable SMS"; }
  [ "$APPROVE2" = "1" ] && { send_sms "$PHONE2_NUMBER" "$SMS_TRIGGER_PHRASE"; log "Phone2 confirmed home - sent hotspot-enable SMS"; }

  CONNECTED_SSID=$(try_connect_to_any_hotspot "$APPROVE1" "$APPROVE2")
  if [ -z "$CONNECTED_SSID" ]; then
    log "No iPhone hotspot appeared within ${HOTSPOT_SCAN_TIMEOUT}s - giving up this cycle, will retry next poll"
    return 1
  fi

  # Give Android's tethering a moment to re-home the upstream, then verify.
  sleep 10
  if ping -c 1 -W 3 8.8.8.8 >/dev/null 2>&1; then
    log "Connected to $CONNECTED_SSID - internet confirmed via borrowed hotspot"
    echo "$CONNECTED_SSID" > /data/adb/dashboard/borrowed_ssid
    set_internet_type "borrowed"
    return 0
  else
    log "Connected to $CONNECTED_SSID but internet not reachable - will retry next poll"
    forget_network_by_ssid "$CONNECTED_SSID"
    return 1
  fi
}

return_to_own_connection() {
  log "Own cellular connection restored - disconnecting from borrowed hotspot"
  BORROWED_SSID=$(cat /data/adb/dashboard/borrowed_ssid 2>/dev/null)
  forget_network_by_ssid "$BORROWED_SSID"
  rm -f /data/adb/dashboard/borrowed_ssid
  set_internet_type "self"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

[ -f "$STATE_FILE" ] || set_internet_type "self"
BAD_COUNT=0

while true; do
  load_config
  CURRENT_STATE=$(cat "$STATE_FILE" 2>/dev/null || echo "self")

  if [ "$CURRENT_STATE" = "borrowed" ]; then
    ACTUAL_CONNECTED_SSID=$(borrowed_connection_alive)
    if [ -z "$ACTUAL_CONNECTED_SSID" ]; then
      log "Borrowed hotspot connection lost (WiFi off, or not connected to either known phone's hotspot) - resetting to self so the dashboard doesn't keep showing a false Borrowed status; will re-attempt failover on the next bad reading if cellular is still down"
      set_internet_type "self"
      rm -f /data/adb/dashboard/borrowed_ssid
      BAD_COUNT=$((CONSECUTIVE_BAD_NEEDED - 1))
      sleep "$CHECK_INTERVAL"
      continue
    fi
    RECORDED_SSID=$(cat /data/adb/dashboard/borrowed_ssid 2>/dev/null)
    if [ "$ACTUAL_CONNECTED_SSID" != "$RECORDED_SSID" ]; then
      log "borrowed_ssid was stale (recorded \"$RECORDED_SSID\", actually connected to \"$ACTUAL_CONNECTED_SSID\") - Android's own network selection must have silently switched between the two saved iPhone hotspots; corrected"
      echo "$ACTUAL_CONNECTED_SSID" > /data/adb/dashboard/borrowed_ssid
    fi

    sleep "$RECOVERY_CHECK_INTERVAL"
    if cellular_reachable; then
      MBPS=$(measure_cellular_mbps)
      if awk -v m="$MBPS" -v t="$LOW_SPEED_THRESHOLD_MBPS" 'BEGIN{exit !(m>=t)}'; then
        return_to_own_connection
        BAD_COUNT=0
      else
        log "Cellular reachable but still slow (${MBPS}Mbps) - staying on borrowed hotspot"
      fi
    else
      log "Cellular still unreachable - staying on borrowed hotspot"
    fi
  else
    sleep "$CHECK_INTERVAL"
    if cellular_reachable; then
      MBPS=$(measure_cellular_mbps)
      if awk -v m="$MBPS" -v t="$LOW_SPEED_THRESHOLD_MBPS" 'BEGIN{exit !(m<t)}'; then
        BAD_COUNT=$((BAD_COUNT + 1))
        log "Low throughput reading (${MBPS}Mbps) - $BAD_COUNT/$CONSECUTIVE_BAD_NEEDED"
      else
        BAD_COUNT=0
      fi
    else
      BAD_COUNT=$((BAD_COUNT + 1))
      log "Cellular unreachable - $BAD_COUNT/$CONSECUTIVE_BAD_NEEDED"
    fi

    if [ "$BAD_COUNT" -ge "$CONSECUTIVE_BAD_NEEDED" ]; then
      trigger_failover
      BAD_COUNT=0
    fi
  fi
done
