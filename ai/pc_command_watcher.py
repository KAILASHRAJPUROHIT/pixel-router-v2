"""
Polls the router dashboard for a queued PC command (restart_pc /
restart_rustdesk) and executes it locally.

This exists because there's no direct phone->PC connectivity path: the
obvious choice (the PC's Tailscale IP) doesn't work here since Tailscale
can't run on the router phone alongside the WireGuard tunnel it already
depends on for Airtel-bypass routing (Android only allows one
VpnService-based VPN active at a time, and WireGuard has that slot). So
instead of the phone reaching out to the PC, the PC polls a small JSON
file (pc_command.json) over the same public dashboard URL/credentials
fetch_router_data.py already uses for stats - no new inbound exposure
on either end.

The dashboard's "Restart RustDesk (PC)" / "Restart PC" buttons
(cgi-bin/pc_command.cgi) write {"command": ..., "ts": <epoch>} to that
file. Each poll compares ts against the last-processed one (persisted
locally) so a command only fires once, however many times it's polled
before being superseded by a new one.

restart_pc queues `shutdown /r /t 60` rather than an immediate reboot -
a 60s grace period gives anyone actually sitting at the PC a chance to
notice the toast and run `shutdown /a` to cancel, since this can be
triggered remotely from anywhere the dashboard is reachable.
"""

import json
import os
import subprocess
import sys
import time

import requests
from requests.auth import HTTPBasicAuth

import config
import notifier

if sys.stdout is None:
    _log_path = os.path.join(os.path.dirname(__file__), "data", "pc_command_watcher_console.log")
    os.makedirs(os.path.dirname(_log_path), exist_ok=True)
    _log_file = open(_log_path, "a", encoding="utf-8", buffering=1)
    sys.stdout = _log_file
    sys.stderr = _log_file

POLL_INTERVAL_SECONDS = 60
STATE_FILE = os.path.join(os.path.dirname(__file__), "data", "pc_command_state.json")


def _log(msg):
    print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {msg}")


def _load_last_processed_ts():
    try:
        with open(STATE_FILE, "r", encoding="utf-8") as f:
            return json.load(f).get("last_processed_ts", 0)
    except (FileNotFoundError, json.JSONDecodeError):
        return 0


def _save_last_processed_ts(ts):
    os.makedirs(os.path.dirname(STATE_FILE), exist_ok=True)
    with open(STATE_FILE, "w", encoding="utf-8") as f:
        json.dump({"last_processed_ts": ts}, f)


def fetch_pc_command():
    """Same tunnel/LAN/stable fallback order as fetch_router_data.py."""
    errors = []
    for base_url in (config.DASHBOARD_URL_STABLE, config.DASHBOARD_URL_LAN, config.DASHBOARD_URL_TUNNEL):
        if not base_url:
            continue
        try:
            resp = requests.get(
                f"{base_url}/pc_command.json",
                auth=HTTPBasicAuth(config.DASHBOARD_AUTH_USER, config.DASHBOARD_AUTH_PASS),
                timeout=10,
            )
            if resp.status_code == 404:
                # Nothing has ever been queued yet - not an error.
                return None
            resp.raise_for_status()
            return resp.json()
        except Exception as e:
            errors.append(f"{base_url}: {e}")
    _log(f"could not reach dashboard: {'; '.join(errors)}")
    return None


def execute_command(command):
    if command == "restart_rustdesk":
        _log("restarting RustDesk service")
        notifier.alert("Router dashboard", "Restarting RustDesk service...")
        result = subprocess.run(
            ["powershell", "-NoProfile", "-Command", "Restart-Service -Name RustDesk -Force"],
            capture_output=True, text=True,
        )
        if result.returncode == 0:
            _log("RustDesk restarted successfully")
        else:
            _log(f"RustDesk restart FAILED: {result.stderr.strip()}")
            notifier.alert("Router dashboard", f"RustDesk restart failed: {result.stderr.strip()[:200]}")

    elif command == "restart_pc":
        _log("queuing PC restart (60s grace period, cancel with `shutdown /a`)")
        notifier.alert("Router dashboard", "PC restart requested remotely - rebooting in 60s. Run `shutdown /a` in a terminal to cancel.")
        subprocess.run(["shutdown", "/r", "/t", "60", "/c", "Restart requested from router dashboard"])

    else:
        _log(f"unknown command: {command!r} - ignoring")


def main():
    _log("pc_command_watcher starting")
    last_processed_ts = _load_last_processed_ts()

    while True:
        data = fetch_pc_command()
        if data:
            ts = data.get("ts", 0)
            command = data.get("command")
            if ts > last_processed_ts:
                _log(f"new command queued: {command} (ts={ts})")
                execute_command(command)
                last_processed_ts = ts
                _save_last_processed_ts(ts)
        time.sleep(POLL_INTERVAL_SECONDS)


if __name__ == "__main__":
    main()
