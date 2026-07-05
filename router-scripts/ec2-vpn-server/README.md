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

## WireGuard

Server-side WireGuard config isn't duplicated here since it contains
the server's private key - see the main project README for how it was
set up (`wg0.conf` under `/etc/wireguard` on the EC2 box).
