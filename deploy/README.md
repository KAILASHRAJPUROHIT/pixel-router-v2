# Deploying V2 (never automatic - always a deliberate step)

V2 is designed to run alongside V1 without touching it: different port
(9090, not 8080), different state directory (`/data/adb/router_v2/`,
not `/data/adb/dashboard/`), different iptables chain names (`TTLPROXYV2`
suffix, never bare `TTLPROXY`), different fwmark/policy-routing table
(`0x2`/`101`, not V1's `0x1`/`100`).

## First-time setup

1. `./deploy/build.sh` - cross-compiles every binary into `deploy/out/`.
2. Generate credentials once: `./deploy/out/setup --db=<local temp path>`
   - prompts for a password, prints the bcrypt hash, a fresh TOTP secret,
     and 10 recovery codes. **Save all of this now** - it's the only time
     the password and recovery codes are ever shown.
3. Add the TOTP secret to Authy manually (enter key, not QR scan).
4. On the device, create the directory tree:
   ```
   /data/adb/router_v2/{bin,state,logs,secrets}
   ```
5. Push the binaries from `deploy/out/` to `/data/adb/router_v2/bin/`.
6. Write the printed password hash to
   `/data/adb/router_v2/secrets/password_hash.txt` and the TOTP secret to
   `/data/adb/router_v2/secrets/totp_secret.txt` - `chmod 600`, root-owned,
   same convention V1 used for its own secrets files.
7. Write your real hotspot SSID/password to
   `/data/adb/router_v2/secrets/hotspot.json`:
   ```json
   { "ssid": "...", "password": "...", "band": "bridged_2_5", "deny_2ghz_macs": [] }
   ```
8. Push `deploy/services.json` to `/data/adb/router_v2/services.json`.
9. Start the supervisor:
   ```
   /data/adb/router_v2/bin/supervisor \
     --manifest=/data/adb/router_v2/services.json \
     --status=/data/adb/router_v2/state/supervisor_status.json \
     --log=/data/adb/router_v2/logs/supervisor.jsonl
   ```

## Before the hotspot service is ever started for real

The `hotspot` service in `services.json` calls the exact same
`stop-softap`/`start-softap` sequence V1 uses on the **same physical
radio** V1 currently manages for real, actively-connected devices.
Starting it will visibly disconnect everyone briefly. Do this
deliberately, not as part of routine testing - confirm with whoever's
using the hotspot first.

## Status

Check `state/supervisor_status.json` for live per-service state (pid,
restart count, last exit code) - written atomically (temp file + rename)
every time it changes, so it's never read mid-write.
