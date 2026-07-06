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
# - iPhones give no external API to remotely flip Personal Hotspot on -
#   the trigger is an SMS containing a fixed phrase, matched by a
#   Shortcuts personal automation the phone's owner sets up themselves
#   ("When I receive a message containing FAILOVER_TRIGGER_PHRASE, turn
#   on Personal Hotspot").
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
load_config() {
  PHONE1_NUMBER=$(grep "^phone1.number=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_SSID=$(grep "^phone1.ssid=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE1_PASSWORD=$(grep "^phone1.password=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_NUMBER=$(grep "^phone2.number=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_SSID=$(grep "^phone2.ssid=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
  PHONE2_PASSWORD=$(grep "^phone2.password=" "$CONFIG" 2>/dev/null | cut -d= -f2-)
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

send_trigger_sms() {
  NUMBER="$1"
  [ -z "$NUMBER" ] && return
  # Must target the receiver explicitly (-n) - confirmed 2026-07-06 that
  # an implicit action-only broadcast never reaches it at all (Android
  # 8+ blocks implicit broadcasts to manifest-declared receivers, even
  # exported ones, specifically to stop apps waking on arbitrary custom
  # actions; explicit-component broadcasts aren't subject to that).
  am broadcast -n com.router.aod/.SendSmsReceiver -a com.router.aod.SEND_SMS --es number "$NUMBER" --es message "$SMS_TRIGGER_PHRASE" >/dev/null 2>&1
  log "Sent failover trigger SMS to $NUMBER"
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

# Scans for either configured iPhone SSID, connects to whichever is
# seen first. Retries within HOTSPOT_SCAN_TIMEOUT since the iPhone
# needs a few seconds to actually enable its hotspot after the SMS.
try_connect_to_any_hotspot() {
  ELAPSED=0
  while [ "$ELAPSED" -lt "$HOTSPOT_SCAN_TIMEOUT" ]; do
    cmd wifi start-scan >/dev/null 2>&1
    sleep 8
    SCAN_RESULTS=$(cmd wifi list-scan-results 2>/dev/null)

    if [ -n "$PHONE1_SSID" ] && echo "$SCAN_RESULTS" | grep -qF "$PHONE1_SSID"; then
      log "Found $PHONE1_SSID - connecting"
      cmd wifi connect-network "$PHONE1_SSID" wpa2 "$PHONE1_PASSWORD" >/dev/null 2>&1
      echo "$PHONE1_SSID"
      return 0
    fi
    if [ -n "$PHONE2_SSID" ] && echo "$SCAN_RESULTS" | grep -qF "$PHONE2_SSID"; then
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
  send_trigger_sms "$PHONE1_NUMBER"
  send_trigger_sms "$PHONE2_NUMBER"

  CONNECTED_SSID=$(try_connect_to_any_hotspot)
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
