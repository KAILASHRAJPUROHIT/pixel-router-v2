# ec2-vpn-server

Backup/reference for what's running on the AWS EC2 box (t3.micro,
`ap-south-1`, Elastic IP `13.205.101.217`) that hosts the WireGuard VPN
server and reverse-proxies the router's dashboard to a permanent URL.

## Dashboard access

Caddy (installed via the official apt repo, running as a systemd
service, auto-restarts on boot) reverse-proxies both a DuckDNS hostname
and a free sslip.io fallback to the phone's dashboard over the existing
WireGuard tunnel (the phone is always `10.66.66.2` on that tunnel):

- **https://antrivarouter.duckdns.org** - the memorable one, a free
  DuckDNS A record pointing at the Elastic IP. Since the IP is static
  (Elastic IP, not the phone's own address), this needed setting only
  once - no dynamic-update client running anywhere.
- **https://13-205-101-217.sslip.io** - free automatic-DNS fallback,
  no account needed, resolves directly from the IP embedded in the
  hostname. Kept as a backup in case DuckDNS ever has an outage.

Caddy auto-manages Let's Encrypt certificates for both. `Caddyfile` in
this directory is the exact config in use - copy it to
`/etc/caddy/Caddyfile` and `sudo systemctl reload caddy` to restore.

Basic auth for the dashboard itself is still enforced by busybox httpd
on the phone (see `aod/dashboard.properties.example` /
`ai/local_secrets.py.example`) - Caddy is just a TLS-terminating proxy,
not a second auth layer.

## Brute-force protection (fail2ban)

Security audit 2026-07-05: the dashboard is now public, and neither
Caddy nor busybox httpd rate-limit or lock out repeated basic-auth
failures on their own. Caddy logs every request as JSON to
`/var/log/caddy/access.log` (configured in `Caddyfile` above), and
fail2ban watches it for repeated 401s:

- `fail2ban/caddy-dashboard-auth.filter.conf` -> copy to
  `/etc/fail2ban/filter.d/caddy-dashboard-auth.conf`
- `fail2ban/caddy-dashboard-auth.jail.conf` -> copy to
  `/etc/fail2ban/jail.d/caddy-dashboard-auth.conf`
- 5 failed logins from the same IP within 10 minutes bans that IP for
  1 hour (firewalled via iptables, fail2ban's default backend).
- `sudo systemctl restart fail2ban` after copying, then
  `sudo fail2ban-client status caddy-dashboard-auth` to confirm the
  jail is active. Test the regex against real log lines first with
  `sudo fail2ban-regex /var/log/caddy/access.log /etc/fail2ban/filter.d/caddy-dashboard-auth.conf`
  before trusting it.

**If you ever get locked out yourself** (e.g. sharing a NAT'd IP on a
mobile network with someone else who trips the ban) - the jail only
covers ports `http`/`https`, it never touches SSH. SSH in and run:
```
sudo fail2ban-client set caddy-dashboard-auth unbanip <your-ip>
```
to lift the ban immediately, or `sudo fail2ban-client status
caddy-dashboard-auth` first if you're not sure which IP got banned.

## WireGuard

Server-side WireGuard config isn't duplicated here since it contains
the server's private key - see the main project README for how it was
set up (`wg0.conf` under `/etc/wireguard` on the EC2 box).

## Remote adb debugging (2026-07-06)

Full phone control from anywhere, not just USB, via three paths:

- **USB** - unchanged, always works when physically connected to a
  laptop.
- **LAN/WiFi** - `adb connect 10.103.106.134:5555` (phone's IP on its
  own hotspot bridge, `ap_br_wlan2`) from a laptop on the same
  hotspot. Only works from the laptop currently named "Laptop" in
  `device_names.conf` (MAC `d4:e9:8a:42:b6:89`) - see below.
- **AWS/internet** - reuses this box's existing WireGuard tunnel
  rather than adding a second VPN (e.g. Tailscale) on the phone. From
  this box: `~/adb-phone.sh shell`, `~/adb-phone.sh push ...`, etc.
  (wraps `adb connect 10.66.66.2:5555` + `adb -s 10.66.66.2:5555 ...`).
  Works from anywhere in the world via `ssh ubuntu@13.205.101.217`,
  independent of whether any laptop is on.

**How it's kept secure**: `adb tcpip 5555` exposes port 5555 on every
interface by design (Android doesn't support binding adbd to a
specific interface) - so it's firewalled at the iptables INPUT chain
instead, matching the same MAC-based model as the per-device
Deny/Allow blocklist:
- tcp/5555 from the laptop's WiFi MAC only (not IP - hotspot DHCP
  leases can change)
- tcp/5555 over `tun0` only (the WireGuard tunnel - not the whole
  internet, just this EC2 box specifically)
- tcp/5555 from loopback
- everything else to tcp/5555 is dropped

This is deliberately narrower than the wireless-ADB setup a
2026-07-05 security audit found and disabled (that one was reachable
by anyone on the hotspot with no restriction at all).

**Auth**: this EC2 box's adb public key (`~/.android/adbkey.pub`
here) is appended directly to the phone's
`/data/misc/adb/adb_keys` (system-trusted keys, root-writable) so it
never needs the on-device "always allow" tap a normal adb connection
prompts for - there's no display attached to approve it anyway. Any
new device that needs adb access this way needs its own pubkey added
the same way.

Both `adb tcpip 5555` and the iptables rules reset on every phone
reboot - `router-scripts/service.d/remote_adb_boot.sh` re-asserts both
at boot (one-shot, same pattern as `mac_blocklist_boot.sh`).
