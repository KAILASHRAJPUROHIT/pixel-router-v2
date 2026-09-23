"""
Reads heartbeat.py's log and prints a simple uptime summary: percent up,
number of outages, and the duration of each. Run anytime - e.g. at the
end of the week-long router trial - via `python uptime_summary.py`.
"""

import json
import os
from datetime import datetime

HEARTBEAT_FILE = os.path.join(os.path.dirname(__file__), "data", "heartbeat.jsonl")


def load_entries():
    if not os.path.exists(HEARTBEAT_FILE):
        return []
    with open(HEARTBEAT_FILE, "r", encoding="utf-8") as f:
        return [json.loads(line) for line in f if line.strip()]


def summarize():
    entries = load_entries()
    if not entries:
        print("No heartbeat data yet - is heartbeat.py running?")
        return

    total = len(entries)
    up_count = sum(1 for e in entries if e["up"])
    pct = 100 * up_count / total

    outages = []
    outage_start = None
    for e in entries:
        ts = datetime.fromisoformat(e["timestamp"])
        if not e["up"]:
            if outage_start is None:
                outage_start = ts
        elif outage_start is not None:
            outages.append((outage_start, ts))
            outage_start = None
    if outage_start is not None:
        outages.append((outage_start, None))  # still down as of the last entry

    print(f"Heartbeat window: {entries[0]['timestamp']} -> {entries[-1]['timestamp']}")
    print(f"Total checks: {total} | Up: {up_count} ({pct:.2f}%) | Down: {total - up_count}")
    print(f"Outages: {len(outages)}")
    for start, end in outages:
        if end is not None:
            dur_min = (end - start).total_seconds() / 60
            print(f"  {start.isoformat()} -> {end.isoformat()}  ({dur_min:.1f} min)")
        else:
            print(f"  {start.isoformat()} -> ONGOING")


if __name__ == "__main__":
    summarize()
