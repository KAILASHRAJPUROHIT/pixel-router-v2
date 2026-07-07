"""
Regression test for RouterDreamService.java's STAT_PATTERN regex - the
one that broke silently twice in one session during the dashboard's
SaaS redesign (2026-07-07), because nothing verified the generated HTML
against what the Android app actually parses until someone noticed the
AOD showing "--" for Health/Internet/Signal/AKD/Custom Message.

Fetches the live dashboard HTML (same tunnel-then-LAN fallback as
fetch_router_data.py) and checks every key the Java side looks up by
name actually parses out. Two different sets, both required:

- STAT_KEYS (RouterDreamService.java): rendered into every one of the
  30 AOD layouts via the row0-row9 grid.
- A handful of additional keys looked up directly by literal string
  elsewhere in the same file, not part of the STAT_KEYS/row loop.

Run directly (`python test_stat_pattern.py`) - prints each key's
pass/fail and exits non-zero if anything is missing, so it can be
wired into a CI check or just run by hand after any change to
update.sh's HTML output.
"""

import sys

from fetch_router_data import fetch_dashboard_html, parse_stats, RouterUnreachable

STAT_KEYS = [
    "Down", "Up", "Devices", "Total Used", "Battery", "Thermal",
    "Skin Temp", "SMS Fwd", "Internet", "Signal",
]

ADDITIONAL_KEYS = [
    "Health", "CPU MHz", "GPU MHz", "Internet Type", "AKD", "Custom Message",
]

REQUIRED_KEYS = STAT_KEYS + ADDITIONAL_KEYS


def main():
    try:
        html, source_url = fetch_dashboard_html()
    except RouterUnreachable as e:
        print(f"FAIL: could not fetch dashboard - {e}")
        return 1

    stats = parse_stats(html)
    print(f"Fetched from {source_url}, {len(stats)} label/value pairs parsed\n")

    missing = []
    for key in REQUIRED_KEYS:
        if key in stats:
            print(f"  OK   {key} = {stats[key]!r}")
        else:
            print(f"  FAIL {key} - not found in parsed HTML")
            missing.append(key)

    print()
    if missing:
        print(f"FAILED: {len(missing)}/{len(REQUIRED_KEYS)} required key(s) missing: {', '.join(missing)}")
        print("This means RouterDreamService.java's STAT_PATTERN regex will silently fail")
        print("to populate these fields on every AOD layout - check update.sh's HTML output")
        print("for stray attributes on the label/value divs (see the regex's own comments).")
        return 1

    print(f"PASS: all {len(REQUIRED_KEYS)} required keys parsed correctly")
    return 0


if __name__ == "__main__":
    sys.exit(main())
