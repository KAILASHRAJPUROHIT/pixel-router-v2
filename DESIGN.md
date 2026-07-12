# Pixel Router V2 — Design Notes

## Relationship to V1

V1 (`D:\AI_PROJECTS\pixel-router`) is **not being retired**. It stays running as
a permanent live fallback, not just a safety net during migration. V2 runs on
a different port (9090) and its own state directory (`/data/adb/router_v2/`)
so the two can coexist indefinitely. If V2 ever crashes or misbehaves, V1 is
still there. Cutover, if it ever happens, is a deliberate choice — not a
requirement for V2 to exist.

## Why Go, not OpenWrt / not s6 or runit

- **OpenWrt on Android**: researched and rejected as the core. `procd` (its
  init) needs PID 1, which is structurally unavailable in a chroot/proot
  alongside Android's own init. Working demos found online don't actually
  replace Android's network stack with OpenWrt's — they run OpenWrt's
  userspace on top of Android's existing WiFi/hostapd/bridging, which is
  functionally what V1 already does directly.
- **s6 / runit**: the architecturally "right" choice for process supervision,
  but both are C codebases, and there is no C compiler anywhere in this
  project's toolchain (confirmed — no NDK, no clang/gcc on the phone, no NDK
  on the dev machine). Porting them would hit the same wall that blocked a
  native TOTP implementation earlier in V1.
- **Go**: cross-compiles to `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` with zero
  C toolchain, producing a fully static binary that runs natively on this
  device — confirmed working end-to-end (build on Windows → push → run on
  phone) for the supervisor, webserver, and a pure-Go SQLite driver
  (`modernc.org/sqlite`, no cgo). One language for the whole stack.

## What's built and tested so far

- `internal/supervisor` — real process supervision via `Wait()`, not polling.
  Exponential backoff on crash-loops (verified live: 1s→2s→4s→8s), atomic
  status-file writes (temp file + rename — direct fix for the corrupted-file
  class of bug found in V1's cleanup pass).
- `internal/totp` — HMAC-SHA1/TOTP re-hosted in Go using stdlib
  `crypto/hmac`+`crypto/sha1`+`encoding/base32`. Passes the same RFC 4226 and
  RFC 6238 test vectors V1's shell implementation was validated against, now
  as an automated test (`go test`) instead of manual live verification.
- `internal/store` — SQLite-backed sessions, login-lockout tracking, and
  one-time recovery codes (closing a real gap: neither V1 nor early V2 had
  any way back into the dashboard if the phone with Authy on it were ever
  lost or reset).
- `internal/webserver` — one `requireSession` middleware gates every
  protected route. V1 had to bolt the equivalent check into 11 separate CGI
  scripts individually; here it's structurally impossible to add a new
  protected route that forgets it.
- `internal/netcontrol` — the actual "is the connection healthy, and if not,
  reconnect" decision logic (`EvaluateAndHeal`) is split from the real
  system-call implementation (`RealController`) via a `Controller` interface,
  with a `FakeController` for tests. All of V1's live-only validation risk on
  this exact logic is now covered by a `go test` suite that runs on Windows,
  no device or root required. The real implementation was separately
  spot-checked against live `dumpsys telephony.registry` output and matches.

## Research findings (GitHub/Reddit deep-dive, 2026-07-11)

- **`pdanet-linux`'s "6-layer" carrier-detection bypass** is more thorough
  than V1's TTL-only defense: (1) TTL normalization, (2) **IPv6 disabled
  entirely** on the tethering path, (3) DNS forced through one gateway point,
  (4) OS-update-domain traffic blocked, (5) MSS/MTU clamped to match the
  phone's own stack, (6) optional traffic shaping.
- **Confirmed live on the actual V1 device**: IPv6 forwarding is enabled,
  the hotspot bridge has a real global-scope IPv6 address, and IPv6 traffic
  is actively being forwarded (437KB seen in one check) with **zero**
  hop-limit normalization — V1's `ttlproxy` is IPv4/TCP-only. Any tethered
  device using IPv6 (common on modern OSes) leaks its real hop-limit straight
  through, undermining the whole defense for that traffic. This was **not
  fixed on V1** per the "don't touch it" instruction — it's flagged here as a
  requirement V2's network-defense layer must close from day one.
- **Recommended fix, per an XDA thread's approach**: force the cellular APN
  to IPv4-only, rather than building a full IPv6 hop-limit-proxy equivalent —
  removes the whole IPv6 leak vector at the root (no IPv6 packets would ever
  traverse the WAN leg to be inspected). Simpler than porting `ttlproxy` to
  IPv6. Worth confirming this APN setting is actually honored by the modem
  before relying on it exclusively; a real IPv6 TPROXY equivalent remains the
  fallback if not.
- **`sing-box`** (used by the `NetProxy-Magisk` project found in this
  research) is a mature, actively-maintained, **Go-based** transparent-proxy
  core built for exactly this tethering/hotspot-sharing use case. Worth
  evaluating as a foundation for V2's traffic-normalization layer instead of
  hand-rolling a new C-free equivalent of `ttlproxy` from scratch — fits the
  Go-only toolchain constraint directly. Not yet integrated; a candidate for
  the next network-defense work.
- **`VPNHotspot` (Mygod)** is a well-known, actively maintained reference for
  doing hotspot/repeater sharing via proper Android `TetheringManager`-style
  APIs — worth reviewing against V1's `TetherStarter.java`/`app_process`
  approach for any hardening ideas, though V1's approach is already confirmed
  working via the `TETHER_PRIVILEGED` permission wall workaround.
- **Airtel Fast Lane** (network-sliced "Priority Postpaid", ₹499–1,749/mo) is
  real and confirmed to exist, but its marketing only addresses **congestion
  prioritization**, not tethering-detection exemption specifically — these
  are separate Airtel mechanisms. Still an open question worth asking Airtel
  support directly: does Fast Lane also exempt tethered/hotspot traffic from
  TTL-based throttling, or only improve raw congestion handling?

## Status (2026-07-11)

Built and unit-tested:
- `internal/supervisor` + `cmd/supervisor` - crash detection, exponential
  backoff, atomic status writes.
- `internal/totp` + `internal/store` + `internal/webserver` + `cmd/webserver` -
  full 2FA login flow (password + live Authy code or one-time recovery code),
  session middleware, per-IP lockout, `/api/devices` and `/api/status`.
- `internal/ttlproxy` + `cmd/ttlproxy` - TPROXY-based TCP relay covering both
  IPv4 and IPv6, chain names deliberately disjoint from V1's (`TTLPROXYV2`,
  mark `0x2`, table `101`, port `12346` vs V1's `TTLPROXY`/`0x1`/`100`/`12345`).
- `internal/hotspot` + `cmd/hotspot` - V1's full dual-band bring-up sequence
  ported faithfully (BandOptimization patch, PMF disable, 2.4GHz deny-ACL,
  hostapd SIGHUP reload), one owning service instead of V1's four independent
  call-sites (so no mutex needed).
- `internal/netcontrol` - cellular health check + guarded reconnect, decision
  logic fully unit-tested via a fake controller.
- `deploy/` - a one-command cross-compile build (`build.sh`), the service
  manifest tying all four daemons together under the supervisor, and a
  README covering first-time setup.

- `internal/usbtether` + `cmd/usbtether` - USB NCM tethering bring-up/
  recovery. The core mechanism (Android's `TetheringManager.startTethering()`,
  the only API that reliably works here) can't be reimplemented in pure Go -
  it needs a Binder call carrying UID 0, which V1 solved via a small compiled
  Java tool (`TetherStarter.dex`) run through `app_process`. Reused that exact
  proven artifact unchanged (copied into `deploy/assets/`, V1's copy never
  touched); Go's role is purely orchestration - shell out, retry with the same
  backoff V1 used, verify via `ncm0` presence. Decision/retry logic unit
  tested via a fake `Starter`; the real `IsUp()` check verified live (only
  ever a safe read-only check).
- `internal/forwarder` + `cmd/forwarder` - SMS/call-forwarder health
  monitoring (enabled state, SMS permissions, Doze whitelist), ported from
  `forwarder_watchdog.sh`. Fully live-tested on the real device, including
  verifying the permission-check regex correctly discriminates real granted
  permissions from ungranted/bogus ones (not a rubber-stamp bug) - safe to
  test live throughout, since every corrective action here (re-grant, re-
  enable, re-whitelist) is idempotent and never disrupts an active connection.

**Not yet live-tested against the real device**: the hotspot bring-up
sequence, and USB-tether's actual `Start()` call (only its read-only `IsUp()`
check was verified live). Unlike every other piece, there's no way to
isolate these tests - both manage physical resources (the one WiFi radio,
the one USB tether link) real devices are actively using right now. Flagged
rather than silently run.

- `internal/quota` + `cmd/quota` - monthly cellular data cutoff, ported from
  `cellular_quota_monitor.sh`. 8 unit tests cover every subtle scenario V1's
  logic handles (byte accumulation, cutoff trigger, no-double-trigger on
  repeated over-cutoff ticks, counter-reset-safe delta calc, billing-day
  rollover, rollover-only-once-per-month guard), including a synthetic
  `/proc/net/dev`-format test proving the parser matches the real column
  layout. Live-verified the safe half end-to-end: real byte reading (~36GB,
  consistent with this session's earlier totals), correctly persisted,
  `cutoff_active` correctly false since real usage is nowhere near 300GB.
  `DisableMobileData()`/`EnableMobileData()` were not live-tested - both
  would cut off the router's entire cellular connection for real if actually
  triggered, same disruption class as hotspot/tether.

- `internal/tunnel` + `cmd/tunnel` - Cloudflare quick-tunnel launch and URL
  capture, fixing V1's confirmed stale-URL race structurally rather than
  mitigating it: V1 grepped an entire never-rotated, ever-growing log file,
  so a restart's very first poll could match a URL from hours earlier before
  the new tunnel had even logged its own. Here, URL extraction reads only the
  fresh subprocess's own stdout/stderr pipe in real time - there is no shared
  file for a stale entry to live in. 5 unit tests, including one that proves
  two independent streams can never influence each other (the actual fix,
  not just the regex). Fully live-tested end to end on real infra: launched
  a genuine new tunnel (completely separate from V1's, different port),
  correctly captured its URL, and confirmed real external HTTPS reachability
  from a network entirely outside the phone's own - a WebFetch from outside
  hit the tunnel URL and got back exactly what a test listener on the phone
  served. Needed one real fix along the way: `SSL_CERT_DIR` wasn't set on
  the subprocess env, so the first live attempt failed identically to a
  known V1 gotcha (Android ships no CA bundle at Go's default paths) -
  confirmed via raw cloudflared output, fixed, re-verified.

- Dashboard frontend - a real HTML/JS page at `/`, plus `/api/health`
  aggregating live hotspot/USB-tether/quota/device-count status. 7 new
  webserver unit tests via `httptest` cover the entire auth flow (401
  without a session, wrong password/code rejected, correct login unlocking
  protected routes, recovery-code single-use, 5-failure lockout, health
  aggregation) - this package had zero automated tests before, despite being
  the security-critical layer. Live-verified on the device: real
  `hotspot_up`/`usb_tether_up` values reflecting actual live state, and the
  dashboard page rendering correctly with a real session.

## AOD app integration (2026-07-11) - resolved and built

Decision: update `com.router.aod` (Java/Kotlin, stays as-is architecturally)
to call V2's API instead of scraping V1's rendered HTML.

- `internal/aodstats` - produces V1's exact 10-stat contract (Down, Up,
  Devices, Total Used, Battery, Thermal, Skin Temp, SMS Fwd, Internet,
  Signal) as clean JSON instead of HTML. 8 unit tests cover the resilience
  behavior (one broken source reports "n/a" without blanking the other 9)
  and every color-threshold rule. Real collectors built for battery
  (`/sys/class/power_supply/battery/capacity`), skin temp (parses
  `dumpsys thermalservice`'s VIRTUAL-SKIN sensor, the same authoritative
  source V1 used), and Down/Up speed (samples `/proc/net/dev` rmnet
  counters twice across a 1s window). Live-verified on the device: all 10
  stats came back with values cross-checked against independently-confirmed
  readings from earlier in this session (Battery 80% exact match, Signal
  -72 dBm exact match, Thermal 43°C correctly triggering the warn
  threshold).
- New `/internal/aod-stats` route on the webserver, gated by a
  `requireLocalhost` middleware (genuine 127.0.0.1/::1 origin check) instead
  of the 2FA session used everywhere else - appropriate since a background
  Android service polling every 5s has no way to interactively enter a
  live Authy code, and this route is structurally unreachable through the
  Cloudflare tunnel or from any other device. 3 new tests cover this
  boundary directly (remote IP forbidden, loopback allowed with no session,
  IPv6 loopback allowed) - the webserver package went from 0 tests to 10
  covering the entire auth surface including this new one, all via
  `httptest`, no device needed.
- `RouterDreamService.java` updated: since the app already polled over HTTP
  (never read files directly), the change was contained to swapping HTML-
  regex-scraping for real JSON parsing via `org.json` (already a
  dependency) - all the downstream UI-binding code was untouched, since it
  only ever read from the same `stats`/`statColors` maps by key name
  regardless of how they got populated. The old Basic-Auth credential
  scheme (`dashboard_auth.properties`, `authUser`/`authPass`,
  `loadCredentials()`) is now fully dead code given the new endpoint's
  origin-based gating, and was removed rather than left unused, along with
  the now-unused HTML regex patterns and the `Base64` import.
  **Verified with a real Gradle build** (`compileDebugJavaWithJavac` and
  `assembleDebug` both succeeded, producing a real signed debug APK) - the
  Android SDK/NDK do exist on this machine at `D:/AndroidSDK` (a genuinely
  useful finding for future work, though the Go-only approach used
  throughout V2 has already proven itself and isn't being revisited).
  **Not yet installed on the device** - deploying this APK replaces the
  AOD app actively running on the phone's screen right now, the same
  live-disruption caution applied to hotspot/USB-tether/quota-disable.

**Not yet built**: borrowed-hotspot failover (`failover_monitor.sh`'s
SMS-trigger + connect-to-a-family-member's-hotspot logic) - the last major
distinct V1 feature not yet ported. A "Health" stat (V1's 10/10
system-health score) also isn't wired into `aodstats` yet - the AOD app
handles its absence gracefully (badge just doesn't update), not a crash.

## Incident log

- **2026-07-11**: while live-testing the new `internal/ttlproxy` package's
  setup routine on the shared device, an early test run used a **stale
  binary** — built before the chain names were renamed from `TTLPROXY` to
  `TTLPROXYV2` to avoid colliding with V1's identically-named live chain.
  The stale binary's `-F TTLPROXY` (flush) succeeded against V1's real chain
  before its `-N TTLPROXY` (create) step failed on "chain already exists,"
  which briefly left V1's actual TPROXY rule wiped (its TTL-normalizing
  protection non-functional for whatever window this went unnoticed).
  Caught immediately via a direct `iptables -L TTLPROXY` check, restored the
  exact original rule (`-A TTLPROXY -p tcp -j TPROXY --on-port 12345
  --tproxy-mark 0x1`), and confirmed the PREROUTING hooks, policy routing,
  and the `ttlproxy` process itself were never touched (the stale binary
  crashed before reaching that code path). **Lesson**: always rebuild
  immediately before testing against the shared device after a rename/
  refactor, and prefer conflict-safe naming (`TTLPROXYV2`) from the very
  first version of a script, not as an afterthought once collision risk is
  noticed.

## Open follow-ups

1. Integrate an IPv6 defense (APN IPv4-only preferred; `sing-box`-based or
   custom IPv6 TPROXY as fallback) before V2's network-defense layer is
   considered complete.
2. Ask Airtel directly about Fast Lane's relationship to tethering detection.
3. Physical cooling (cooling stand/fan, case removal) remains the
   highest-leverage thermal fix available — a hardware change, not a code one.
