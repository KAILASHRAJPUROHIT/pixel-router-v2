"""
Lightweight, fast router-reachability heartbeat - deliberately decoupled
from the heavier Ollama-analysis loop in analyzer.py so outage detection
isn't limited to that loop's 15-minute cadence. Just checks reachability
(no LLM call) every HEARTBEAT_INTERVAL_SECONDS and logs to a rolling
jsonl file, alerting only on state transitions (down after N consecutive
failures, back-up after a real outage) rather than every single check.
"""

import json
import os
import sys
import time
import traceback
from datetime import datetime, timezone

import notifier
from fetch_router_data import get_current_stats, RouterUnreachable

# Runs via pythonw.exe (no console window) as a Scheduled Task - pythonw
# has no console for print() to write to, so redirect stdout/stderr to a
# log file instead of silently losing output. Only kicks in when there's
# no real console attached (sys.stdout is None under pythonw); running
# this script directly in a terminal still prints normally.
if sys.stdout is None:
    _log_path = os.path.join(os.path.dirname(__file__), "data", "heartbeat_console.log")
    os.makedirs(os.path.dirname(_log_path), exist_ok=True)
    _log_file = open(_log_path, "a", encoding="utf-8", buffering=1)
    sys.stdout = _log_file
    sys.stderr = _log_file

HEARTBEAT_INTERVAL_SECONDS = 60
DOWN_ALERT_THRESHOLD = 3  # consecutive failed checks (~3 min) before alerting

HEARTBEAT_FILE = os.path.join(os.path.dirname(__file__), "data", "heartbeat.jsonl")
HEARTBEAT_MAX_ENTRIES = 10080  # ~1 week at a 60s interval


def _append(entry):
    os.makedirs(os.path.dirname(HEARTBEAT_FILE), exist_ok=True)
    lines = []
    if os.path.exists(HEARTBEAT_FILE):
        with open(HEARTBEAT_FILE, "r", encoding="utf-8") as f:
            lines = f.readlines()
    lines.append(json.dumps(entry) + "\n")
    lines = lines[-HEARTBEAT_MAX_ENTRIES:]
    with open(HEARTBEAT_FILE, "w", encoding="utf-8") as f:
        f.writelines(lines)


def run_once(consecutive_down):
    timestamp = datetime.now(timezone.utc).isoformat()
    try:
        get_current_stats()
        _append({"timestamp": timestamp, "up": True})
        if consecutive_down >= DOWN_ALERT_THRESHOLD:
            notifier.alert(
                "Router Back Up",
                f"Reachable again after {consecutive_down} failed checks (~{consecutive_down} min outage).",
            )
        return 0
    except RouterUnreachable as e:
        _append({"timestamp": timestamp, "up": False, "detail": str(e)[:200]})
        consecutive_down += 1
        if consecutive_down == DOWN_ALERT_THRESHOLD:
            notifier.alert(
                "Router Down",
                f"{consecutive_down} consecutive reachability checks have failed (~{consecutive_down} min) - looks like a real outage.",
            )
        return consecutive_down


def main():
    print(f"Starting router heartbeat - checking every {HEARTBEAT_INTERVAL_SECONDS}s")
    consecutive_down = 0
    while True:
        try:
            consecutive_down = run_once(consecutive_down)
        except Exception:
            traceback.print_exc()
        time.sleep(HEARTBEAT_INTERVAL_SECONDS)


if __name__ == "__main__":
    main()
