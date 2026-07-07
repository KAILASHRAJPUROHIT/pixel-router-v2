"""
Central config for the router AI layer. Everything here runs locally on
this machine (RTX 4080 box) against a local Ollama instance - the router
phone stays a dumb, deterministic device with zero AI dependency of its
own. This layer is best-effort: the phone must keep working standalone
whether or not this machine/Ollama happens to be reachable.
"""

import os

# Filled in once the Cloudflare quick-tunnel is live (see
# D:\AI_PROJECTS memory: pixel6-hotspot-router.md). Falls back to the
# phone's local hotspot LAN address when on the same network - update
# DASHBOARD_URL_LAN if the phone's AP-side IP differs.
# Default is today's live quick-tunnel URL (2026-07-04) - this changes
# whenever the phone reboots and cloudflared restarts, since it's an
# unauthenticated quick tunnel (no fixed domain). Override via env var
# once it rotates: check /data/adb/dashboard/tunnel_url.txt on the phone.
DASHBOARD_URL_TUNNEL = os.environ.get(
    "ROUTER_TUNNEL_URL", "https://falling-managed-hottest-contained.trycloudflare.com"
)
DASHBOARD_URL_LAN = os.environ.get("ROUTER_LAN_URL", "http://192.168.43.1:8080")
# The WireGuard/Caddy/DuckDNS path - a fixed hostname, unlike the
# quick-tunnel URL above which rotates every time cloudflared restarts
# (confirmed stale as a hardcoded default more than once already) and
# has its own occasional connection hiccups. Prefer this one first.
DASHBOARD_URL_STABLE = os.environ.get("ROUTER_STABLE_URL", "https://antrivarouter.duckdns.org")

# Credentials come from local_secrets.py (gitignored, not committed) -
# see local_secrets.py.example. Deliberately not named "secrets.py" -
# that would shadow Python's own stdlib secrets module.
try:
    from local_secrets import DASHBOARD_AUTH_USER, DASHBOARD_AUTH_PASS
except ImportError:
    DASHBOARD_AUTH_USER = "changeme"
    DASHBOARD_AUTH_PASS = "changeme"

OLLAMA_HOST = "http://localhost:11434"
OLLAMA_MODEL = "qwen3:14b"

# How often the analyzer loop polls the router and re-runs analysis.
POLL_INTERVAL_SECONDS = 15 * 60

# Rolling history window kept for trend/predictive analysis (jsonl file).
HISTORY_FILE = os.path.join(os.path.dirname(__file__), "data", "history.jsonl")
HISTORY_MAX_ENTRIES = 500  # ~5 days at a 15-min poll interval

INSIGHTS_FILE = os.path.join(os.path.dirname(__file__), "data", "insights.jsonl")
INSIGHTS_MAX_ENTRIES = 200

WEB_HOST = "127.0.0.1"
WEB_PORT = 8765
