# pixel-router

Turning a used Pixel 6 into a permanent, always-on 5G/4G hotspot router, plus tooling to monitor and display its status.

## Structure

- **`aod/`** — Android app implementing a custom always-on display (Daydream/DreamService) that mirrors the router's live status natively on the phone's lock screen: connection speed, connected devices, data usage, battery, thermal state, SMS-forwarder health, internet reachability, signal quality, and a combined health traffic-light. Six selectable layouts (corner stack, centered radial, split halves + grid, sidebar rail, ticker marquee, widget cascade) and eight color themes (neon cyberpunk, glass frost, retro CRT terminal, luxury gold, vaporwave sunset, e-ink newspaper, holographic, nature organic), picked via `AodSettingsActivity` (launcher icon or the gear next to the entry in Settings → Display → Screen saver). Includes automatic safety cutoffs (pauses itself above 45°C or below 30% battery), adaptive day/night brightness, and OLED burn-in protection (periodic pixel-shift).
- **`ai/`** — Local Ollama-based monitoring layer that polls the router's dashboard, keeps a rolling history, flags anomalies, answers natural-language questions about router health, and posts desktop alerts. Also includes `heartbeat.py` (fast 60s reachability check, decoupled from the heavier analysis loop, with its own up/down desktop alerting) and `uptime_summary.py` (prints an outage/uptime report from the heartbeat log). Runs independently of the phone — the router itself stays a dumb, deterministic device with no AI dependency of its own. Both `analyzer.py` and `heartbeat.py` run persistently via Windows Scheduled Tasks, launched with `pythonw.exe` (no console window) with their output redirected to `data/*_console.log`.
- **VPN (WireGuard)** — a self-hosted WireGuard server on an AWS EC2 t3.micro in `ap-south-1` (Mumbai), reachable at a fixed Elastic IP so it never changes even across instance stop/start. The phone's own traffic runs through it (official WireGuard Android app, tunnel name `pxlvpn`). Tethered hotspot-client traffic (laptop, TV, etc.) is also routed through the tunnel via `router-scripts/service.d/vpn_tether_routing.sh` — this was needed because Android's native Wi-Fi hotspot tethering never routes through app VPNs by policy, and VPNHotspot's app-level "Repeater"/"Temporary hotspot" modes don't fit a cellular-only upstream. The script does it directly with policy routing (mark wlan2-sourced packets, route them via a table pointing at tun0, masquerade onto the tunnel) and is boot-persistent, waiting for both interfaces to exist before applying. Also hardened against Airtel's carrier-side tethering entitlement check (confirmed active via `dumpsys tethering`) by disabling `com.google.android.tetheringentitlement` and setting `net.tethering.noprovisioning=true` in `hotspot_on_boot.sh` — belt-and-suspenders since the boot script already avoids the official Tethering API path that would trigger it.

## Setup

Both subprojects need real dashboard credentials that are **not** committed to this repo:

- `aod/dashboard.properties` — copy from `aod/dashboard.properties.example` and fill in your router dashboard's basic-auth username/password.
- `ai/local_secrets.py` — copy from `ai/local_secrets.py.example` and fill in the same credentials.

See each subproject's own files for further details on running/building.
