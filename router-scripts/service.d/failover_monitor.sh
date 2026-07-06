#!/system/bin/sh
# Detects the router's own cellular connection going bad (quota
# exhausted, carrier outage, etc.) and helps fail over to a family
# member's iPhone Personal Hotspot so devices on our own hotspot
# (PILLYBOO) keep working, instead of everyone silently losing internet.
#
# Simplified 2026-07-06 to drop the iPhone Shortcuts/SMS handshake
# entirely (WHEREAREYOU location query + remote hotspot-enable trigger)
# - iPhones give no real API to flip Personal Hotspot on remotely, and
# the two-phase SMS/GPS dance was extra complexity and SMS-quota cost
# for what's fundamentally a problem a human can solve in one tap. Now:
# presence is checked via WiFi (instant) then Bluetooth (bt_presence_
# check, ~15s scan via BtScanService in the AOD app - there's no BT scan
# CLI on this Android build). If a known phone is present, the AOD
# screen just tells you to turn its hotspot on yourself
# (failover_prompt.txt, picked up by update.sh/RouterDreamService's
# existing full-screen message overlay); if neither is present, it says
# so. Once the hotspot appears, this script still auto-detects and
# connects to it the same way as before - only the "ask the phone to
# turn it on" step became a human instruction instead of an SMS.
#
# How the handoff itself still works:
# - This phone's hardware confirmed to support simultaneous AP+STA
#   (`cmd wifi get-softap-supported-features` -> wifi_softap_bridged_ap_
#   with_sta_supported, dumpsys wifi -> "STA + AP Concurrency Supported:
#   true") - so our own hotspot keeps running the whole time; we're just
#   adding a second, upstream Wi-Fi connection alongside it.
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
PROMPT_FILE=/data/adb/dashboard/failover_prompt.txt

CHECK_INTERVAL=120          # 2 min between checks while on our own connection
LOW_SPEED_THRESHOLD_MBPS=1
CONSECUTIVE_BAD_NEEDED=3    # ~6 min of sustained bad readings before failing over
RECOVERY_CHECK_INTERVAL=300 # 5 min between checks while borrowed
HOTSPOT_SCAN_TIMEOUT=180    # give a manually-enabled hotspot up to 3 min to appear
BT_SCAN_WAIT=16             # BtScanService's own discovery window is ~14s

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

set_internet_type() {
  echo "$1" > "$STATE_FILE"
}

set_prompt() {
  echo "$1" > "$PROMPT_FILE"
}

clear_prompt() {
  rm -f "$PROMPT_FILE"
}

# Config file (gitignored, same pattern as dashboard.properties):
#   phone1.name=Kailash
#   phone1.ssid=iPhone (Kailash)
#   phone1.password=xxxxxxxx
#   phone1.mac=xx:xx:xx:xx:xx:xx     (WiFi MAC, as seen connected to PILLYBOO)
#   phone1.bt_mac=xx:xx:xx:xx:xx:xx  (Bluetooth MAC, once paired - separate from WiFi MAC)
#   phone2.name=Beena
#   phone2.ssid=iPhone (Wife)
#   phone2.password=xxxxxxxx
#   phone2.mac=xx:xx:xx:xx:xx:xx
#   phone2.bt_mac=xx:xx:xx:xx:xx:xx
load_config() {
  PHONE1_NAME=$(grep "^phone1.name=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_SSID=$(grep "^phone1.ssid=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_PASSWORD=$(grep "^phone1.password=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_MAC=$(grep "^phone1.mac=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_BT_MAC=$(grep "^phone1.bt_mac=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_NAME=$(grep "^phone2.name=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_SSID=$(grep "^phone2.ssid=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_PASSWORD=$(grep "^phone2.password=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_MAC=$(grep "^phone2.mac=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_BT_MAC=$(grep "^phone2.bt_mac=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  [ -z "$PHONE1_NAME" ] && PHONE1_NAME="Phone 1"
  [ -z "$PHONE2_NAME" ] && PHONE2_NAME="Phone 2"
}

detect_ap_if() {
  if ip addr show ap_br_wlan2 >/dev/null 2>&1; then
    echo ap_br_wlan2
  else
    echo wlan2
  fi
}

# Tier 1, instant: is this MAC currently a connected client on our OWN
# hotspot right now? Only catches phones that happen to have WiFi on -
# both phones normally keep it off at home to avoid loading the router,
# so this is a "best case" check, not the primary mechanism.
wifi_presence_check() {
  MAC="$1"
  [ -z "$MAC" ] && return 1
  AP_IF=$(detect_ap_if)
  ip neigh show dev "$AP_IF" 2>/dev/null | grep -qi "$MAC"
}

# Tier 2, ~16s: Bluetooth classic discovery via BtScanService (no BT
# scan CLI exists on this Android build - see the app_process/Java
# comment in that file). Catches a phone that's home with WiFi off but
# Bluetooth on, which is the common case here. Requires the phone
# already paired with this device once in Bluetooth settings - a scan
# alone finds any nearby discoverable device, but that's not enough to
# confirm which specific phone it is.
bt_presence_check() {
  BT_MAC="$1"
  [ -z "$BT_MAC" ] && return 1
  [ "$(settings get global bluetooth_on 2>/dev/null)" = "1" ] || return 1
  am start-service -n com.router.aod/.BtScanService >/dev/null 2>&1
  sleep "$BT_SCAN_WAIT"
  RESULT_FILE=/data/data/com.router.aod/files/bt_scan_result.txt
  [ -f "$RESULT_FILE" ] || return 1
  grep -qi "$BT_MAC" "$RESULT_FILE"
}

# Tries WiFi first (instant), only falls through to the ~16s Bluetooth
# scan if WiFi didn't confirm it.
presence_check() {
  WIFI_MAC="$1"
  BT_MAC="$2"
  wifi_presence_check "$WIFI_MAC" && return 0
  bt_presence_check "$BT_MAC"
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

# Scans for either APPROVED iPhone SSID (i.e. one confirmed present by
# presence_check - $1/$2 are "1" if phone1/phone2 is approved), connects
# to whichever is seen first. Retries within HOTSPOT_SCAN_TIMEOUT to
# give the human time to actually walk over and flip Personal Hotspot on
# after seeing the AOD prompt.
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
  log "Cellular down/slow for $CONSECUTIVE_BAD_NEEDED consecutive checks - checking presence"

  APPROVE1=0; APPROVE2=0
  presence_check "$PHONE1_MAC" "$PHONE1_BT_MAC" && { APPROVE1=1; log "$PHONE1_NAME confirmed present"; }
  presence_check "$PHONE2_MAC" "$PHONE2_BT_MAC" && { APPROVE2=1; log "$PHONE2_NAME confirmed present"; }

  if [ "$APPROVE1" = "0" ] && [ "$APPROVE2" = "0" ]; then
    log "Neither phone present - showing generic down message, will re-check next poll"
    set_prompt "Main connection down - no backup device home"
    return 1
  fi

  if [ "$APPROVE1" = "1" ] && [ "$APPROVE2" = "1" ]; then
    set_prompt "Main connection down - turn on $PHONE1_NAME's or $PHONE2_NAME's hotspot"
  elif [ "$APPROVE1" = "1" ]; then
    set_prompt "Main connection down - turn on $PHONE1_NAME's hotspot"
  else
    set_prompt "Main connection down - turn on $PHONE2_NAME's hotspot"
  fi

  CONNECTED_SSID=$(try_connect_to_any_hotspot "$APPROVE1" "$APPROVE2")
  if [ -z "$CONNECTED_SSID" ]; then
    log "No hotspot appeared within ${HOTSPOT_SCAN_TIMEOUT}s - giving up this cycle, will retry next poll"
    return 1
  fi

  # Give Android's tethering a moment to re-home the upstream, then verify.
  sleep 10
  if ping -c 1 -W 3 8.8.8.8 >/dev/null 2>&1; then
    log "Connected to $CONNECTED_SSID - internet confirmed via borrowed hotspot"
    echo "$CONNECTED_SSID" > /data/adb/dashboard/borrowed_ssid
    set_internet_type "borrowed"
    clear_prompt
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
  clear_prompt
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
        # Cellular's genuinely fine - clear any leftover prompt from a
        # previous failed borrow attempt rather than leaving a stale
        # "turn on hotspot" message showing once it's no longer needed.
        [ "$BAD_COUNT" -gt 0 ] && clear_prompt
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
