#!/system/bin/sh
# Routes tethered hotspot-client traffic (wlan2) through the WireGuard
# tunnel (tun0) instead of raw cellular (rmnet1). Needed because:
# - Android's native Wi-Fi hotspot tethering never routes through app
#   VPNs by design (a deliberate OS policy), even when the VPN is active
#   and set as the phone's own default route.
# - VPNHotspot's app-level "Repeater"/"Temporary Wi-Fi hotspot" modes
#   were tried and don't fit this setup (Repeater needs an upstream
#   Wi-Fi network, not cellular; Temporary hotspot can't keep a fixed
#   SSID/password across restarts - confirmed 2026-07-05).
# So this does it directly via policy routing: mark packets arriving on
# wlan2, route marked packets via a table pointing at tun0, then
# masquerade them onto the tunnel (required since the WireGuard peer's
# AllowedIPs=10.66.66.2/32 on the AWS side only accepts that single
# source address - unmasqueraded tethered-client IPs would be dropped).

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done

# Wait for the WireGuard tunnel to come up (it's a foreground app the
# user toggles - not guaranteed to be up immediately at boot). Give it
# up to 5 minutes; if it never appears, skip silently (VPN tethering
# just won't apply until the user manually reconnects the tunnel, which
# already re-triggers wg-quick's own routes but not this script - rerun
# manually via `su -c sh /data/adb/service.d/vpn_tether_routing.sh` if
# reconnecting the tunnel long after boot).
i=0
while [ ! -d /sys/class/net/tun0 ] && [ "$i" -lt 150 ]; do
  sleep 2
  i=$((i + 1))
done
[ ! -d /sys/class/net/tun0 ] && exit 0

# Wait for the hotspot AP interface similarly (up to 2 minutes).
i=0
while [ ! -d /sys/class/net/wlan2 ] && [ "$i" -lt 60 ]; do
  sleep 2
  i=$((i + 1))
done
[ ! -d /sys/class/net/wlan2 ] && exit 0

FWMARK=0x9999
TABLE=90

ip rule del fwmark $FWMARK table $TABLE 2>/dev/null
ip route flush table $TABLE 2>/dev/null
iptables -t mangle -D PREROUTING -i wlan2 -j MARK --set-mark $FWMARK 2>/dev/null
iptables -t nat -D POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null
iptables -D FORWARD -i wlan2 -o tun0 -j ACCEPT 2>/dev/null
iptables -D FORWARD -i tun0 -o wlan2 -j ACCEPT 2>/dev/null

ip rule add fwmark $FWMARK table $TABLE priority 100
ip route add default dev tun0 table $TABLE
iptables -t mangle -A PREROUTING -i wlan2 -j MARK --set-mark $FWMARK
iptables -t nat -A POSTROUTING -o tun0 -j MASQUERADE
iptables -I FORWARD 1 -i wlan2 -o tun0 -j ACCEPT
iptables -I FORWARD 1 -i tun0 -o wlan2 -j ACCEPT

echo "$(date '+%Y-%m-%d %H:%M:%S') - VPN tether routing applied (wlan2 -> tun0)" >> /data/adb/dashboard/vpn_tether_routing.log
