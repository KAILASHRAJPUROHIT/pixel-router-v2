# router-scripts

Backup/reference copies of the shell scripts actually running on the router phone itself (rooted Pixel 6, via Magisk `service.d`). These are **not deployed from this repo** — they're pushed to the device manually via `adb push` + `adb shell su -c 'cp ...'`, same as they were originally installed. This directory exists purely so the on-device automation has version control and isn't only living as a single copy on the phone's storage.

- `service.d/` — mirrors `/data/adb/service.d/` on the phone (boot-time services: hotspot persistence, thermal monitor, connection optimizer, conntrack tuning, dashboard server, Cloudflare tunnel, SMS-forwarder watchdog, CPU/GPU underclock, QoS priority).
- `update.sh` — mirrors `/data/adb/dashboard/update.sh`, the dashboard HTML generator loop.

**Redacted for this repo**: a couple of hardcoded *fallback* SSID/password values (rarely-hit last-resort defaults used only if the real WifiConfigStoreSoftAp.xml config is unreadable) are replaced with `CHANGEME_SSID`/`CHANGEME_PASSWORD` placeholders here. The actual on-device copies have the real values — this is just about not committing them in plaintext to git.
