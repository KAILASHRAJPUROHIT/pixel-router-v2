#!/system/bin/sh
# One-shot boot script: re-enables adb-over-TCP (port 5555) and
# firewalls it down to only the two trusted paths this router actually
# needs, added 2026-07-06 for full remote debugging (USB + LAN WiFi +
# AWS over the existing WireGuard tunnel) without repeating the
# wireless-ADB exposure a 2026-07-05 security audit already found and
# disabled (wide open to anyone on the hotspot).
#
# Neither `adb tcpip` mode nor iptables rules survive a reboot on
# their own, so both need re-asserting here every boot:
#   - tcp/5555 from this laptop's WiFi MAC only (d4:e9:8a:42:b6:89,
#     "Laptop" in device_names.conf) - MAC-based, not IP-based, since
#     DHCP-assigned IPs on the hotspot can change.
#   - tcp/5555 over tun0 (the WireGuard tunnel to the AWS EC2 box,
#     10.66.66.2 on the phone side) - lets the EC2 box run
#     `adb connect 10.66.66.2:5555` from anywhere on the internet via
#     SSH, reusing the tunnel that already carries the dashboard,
#     rather than adding a second VPN (e.g. Tailscale) on the phone.
#   - loopback, for completeness.
#   - everything else to tcp/5555 is dropped, same posture as the
#     MAC-based device blocklist elsewhere in this project.
LOG=/data/adb/dashboard/remote_adb_boot.log
LAPTOP_MAC=d4:e9:8a:42:b6:89

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 20

setprop service.adb.tcp.port 5555
stop adbd
start adbd

# Idempotent: -C checks first so re-running this (e.g. a manual retry)
# doesn't pile up duplicate rules. Each successful insert goes to
# position 1, pushing earlier inserts down - so these must run in
# reverse of the desired final top-to-bottom order (DROP first, ending
# up at the bottom; the laptop MAC ACCEPT last, ending up on top)
# or the DROP would end up above the ACCEPTs and block everything.
iptables -C INPUT -p tcp --dport 5555 -j DROP 2>/dev/null || iptables -I INPUT 1 -p tcp --dport 5555 -j DROP
iptables -C INPUT -p tcp --dport 5555 -i lo -j ACCEPT 2>/dev/null || iptables -I INPUT 1 -p tcp --dport 5555 -i lo -j ACCEPT
iptables -C INPUT -p tcp --dport 5555 -i tun0 -j ACCEPT 2>/dev/null || iptables -I INPUT 1 -p tcp --dport 5555 -i tun0 -j ACCEPT
iptables -C INPUT -p tcp --dport 5555 -m mac --mac-source "$LAPTOP_MAC" -j ACCEPT 2>/dev/null || iptables -I INPUT 1 -p tcp --dport 5555 -m mac --mac-source "$LAPTOP_MAC" -j ACCEPT

log "adb tcpip 5555 re-enabled, port firewalled to laptop MAC + tun0 + lo"
