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

## WireGuard

Server-side WireGuard config isn't duplicated here since it contains
the server's private key - see the main project README for how it was
set up (`wg0.conf` under `/etc/wireguard` on the EC2 box).
