#!/system/bin/sh
# Conditionally routes tethered hotspot-client traffic (wlan2) through
# the WireGuard tunnel (tun0) - rewritten 2026-07-07 from a one-shot
# boot script into a persistent watchdog after a real incident: the old
# version applied this routing ONCE at boot and never revisited it, so
# when the tunnel later went dead (confirmed via zero replies from the
# AWS peer in both directions) it kept silently black-holing hotspot
# client traffic instead of falling back.
#
# WHY this routing exists at all: Android's native Wi-Fi hotspot
# tethering never routes through app VPNs by design, even when the VPN
# is the phone's own default route (confirmed 2026-07-05,
# VPNHotspot's Repeater/Temporary-hotspot modes don't fit this setup
# either). Airtel's tethering/hotspot detection can see and throttle
# raw tethered traffic riding directly over cellular - masking it
# through the WireGuard tunnel first is what evades that. This is ONLY
# a concern while the phone's own cellular data is the actual uplink
# (internet_type=self) - Airtel has no visibility at all into a
# WiFi-to-WiFi bridge when borrowing a family member's hotspot
# (internet_type=borrowed), so forcing that traffic through the tunnel
# too would be both pointless and dangerous if the tunnel is ever down.
#
# So this now continuously re-evaluates two things every cycle:
#   1. internet_type (self vs borrowed) - only bypass-route when self.
#   2. Whether the tunnel is actually alive, not just present - a dead
#      tun0 interface can sit there indefinitely (confirmed: TX kept
#      incrementing from our own PersistentKeepalive while RX stayed
#      completely frozen - the asymmetric-silence signature of a dead
#      peer, not a genuinely absent one). Never route real traffic into
#      a tunnel that isn't actually getting replies.
#
# Dead-detection was previously passive - it correctly stopped routing
# into a dead tunnel but never tried to revive it, so recovery meant
# someone noticing and manually unlocking the phone to toggle the
# WireGuard app's GUI switch. The WireGuard app's TunnelManager exposes
# SET_TUNNEL_DOWN/SET_TUNNEL_UP as explicit-component broadcasts
# (confirmed live 2026-07-07 via `am broadcast`, result=0, tun0
# unaffected by the up-call on an already-live tunnel) - these work
# from a root shell with the screen off/locked, no GUI interaction
# needed, removing that limitation entirely.
STATE_DIR=/data/adb/dashboard
LOG="$STATE_DIR/vpn_tether_routing.log"
TUNNEL_ALIVE_FILE="$STATE_DIR/tunnel_alive"
FWMARK=0x9999
TABLE=90
# Longer than PersistentKeepalive=25 in pxlvpn.conf - one full cycle at
# this interval is guaranteed to span at least one keepalive round-trip
# if the tunnel is genuinely alive, avoiding false "dead" reads from
# checking too soon after our own keepalive just went out.
CHECK_INTERVAL=30
WG_RECEIVER="com.wireguard.android/.model.TunnelManager\$IntentReceiver"
WG_TUNNEL_NAME=pxlvpn
DEAD_STREAK_BEFORE_RECONNECT=3
RECONNECT_COOLDOWN=300

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

apply_bypass_routing() {
  [ -d /sys/class/net/wlan2 ] || return 1
  ip rule show 2>/dev/null | grep -q "fwmark $FWMARK lookup $TABLE" || ip rule add fwmark $FWMARK table $TABLE priority 100
  ip route replace default dev tun0 table $TABLE 2>/dev/null
  iptables -t mangle -C PREROUTING -i wlan2 -j MARK --set-mark $FWMARK 2>/dev/null || iptables -t mangle -A PREROUTING -i wlan2 -j MARK --set-mark $FWMARK
  iptables -t nat -C POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -o tun0 -j MASQUERADE
  iptables -C FORWARD -i wlan2 -o tun0 -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -i wlan2 -o tun0 -j ACCEPT
  iptables -C FORWARD -i tun0 -o wlan2 -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -i tun0 -o wlan2 -j ACCEPT
}

remove_bypass_routing() {
  iptables -t mangle -D PREROUTING -i wlan2 -j MARK --set-mark $FWMARK 2>/dev/null
  ip rule del fwmark $FWMARK table $TABLE 2>/dev/null
  iptables -t nat -D POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null
  iptables -D FORWARD -i wlan2 -o tun0 -j ACCEPT 2>/dev/null
  iptables -D FORWARD -i tun0 -o wlan2 -j ACCEPT 2>/dev/null
}

reconnect_tunnel() {
  log "attempting tunnel reconnect via WireGuard app broadcast (tunnel dead for $((DEAD_STREAK_BEFORE_RECONNECT * CHECK_INTERVAL))s+)"
  am broadcast -n "$WG_RECEIVER" -a com.wireguard.android.action.SET_TUNNEL_DOWN --es tunnel "$WG_TUNNEL_NAME" >/dev/null 2>&1
  sleep 3
  am broadcast -n "$WG_RECEIVER" -a com.wireguard.android.action.SET_TUNNEL_UP --es tunnel "$WG_TUNNEL_NAME" >/dev/null 2>&1
  log "reconnect broadcast sent - next cycle's RX check will tell if it worked"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

CURRENTLY_BYPASSING=0
PREV_RX=""
DEAD_STREAK=0
LAST_RECONNECT_ATTEMPT=0

while true; do
  INTERNET_TYPE=$(cat "$STATE_DIR/internet_type" 2>/dev/null)

  TUNNEL_ALIVE=0
  if [ -d /sys/class/net/tun0 ]; then
    CUR_RX=$(cat /sys/class/net/tun0/statistics/rx_bytes 2>/dev/null)
    if [ -n "$PREV_RX" ] && [ -n "$CUR_RX" ] && [ "$CUR_RX" -gt "$PREV_RX" ] 2>/dev/null; then
      TUNNEL_ALIVE=1
    fi
    PREV_RX=$CUR_RX
  else
    PREV_RX=""
  fi
  echo "$TUNNEL_ALIVE" > "$TUNNEL_ALIVE_FILE"

  # Only try to revive the tunnel while it's actually supposed to be
  # doing something (self-cellular uplink) - no point reconnecting a
  # tunnel nothing currently needs, and it'd just be extra battery/radio
  # use while borrowing a family member's hotspot.
  if [ "$INTERNET_TYPE" = "self" ] && [ "$TUNNEL_ALIVE" = "0" ]; then
    DEAD_STREAK=$((DEAD_STREAK + 1))
  else
    DEAD_STREAK=0
  fi

  if [ "$DEAD_STREAK" -ge "$DEAD_STREAK_BEFORE_RECONNECT" ]; then
    NOW=$(date +%s)
    if [ "$((NOW - LAST_RECONNECT_ATTEMPT))" -ge "$RECONNECT_COOLDOWN" ]; then
      reconnect_tunnel
      LAST_RECONNECT_ATTEMPT=$NOW
    fi
    DEAD_STREAK=0
  fi

  if [ "$INTERNET_TYPE" = "self" ] && [ "$TUNNEL_ALIVE" = "1" ]; then
    if [ "$CURRENTLY_BYPASSING" = "0" ]; then
      apply_bypass_routing
      CURRENTLY_BYPASSING=1
      log "On own cellular with a live tunnel - routing hotspot traffic through it (Airtel tethering-detection bypass active)"
    fi
  else
    if [ "$CURRENTLY_BYPASSING" = "1" ]; then
      remove_bypass_routing
      CURRENTLY_BYPASSING=0
      if [ "$INTERNET_TYPE" = "self" ]; then
        log "On own cellular but the tunnel just went dead - falling back to direct routing (Airtel may see raw tethered traffic until it recovers) rather than black-holing hotspot clients"
      else
        log "Internet type is $INTERNET_TYPE, not self-cellular - no Airtel detection concern, using direct routing"
      fi
    fi
  fi

  sleep "$CHECK_INTERVAL"
done
