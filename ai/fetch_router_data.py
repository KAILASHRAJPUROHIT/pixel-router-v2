"""
Pulls current stats + recent activity from the router phone's dashboard.

The phone's busybox-httpd dashboard only serves an HTML page today (no
JSON endpoint yet) - this scrapes the known `<div class="stat">` blocks
from D:\\AI_PROJECTS memory: pixel6-hotspot-router.md's update.sh template.
TODO next time the phone is reachable via adb: add a stats.json export
alongside index.html in update.sh so this can stop scraping HTML.
"""

import re
import requests
from requests.auth import HTTPBasicAuth

import config

STAT_RE = re.compile(
    r'<div class="label">([^<]+)</div><div class="value[^"]*"[^>]*>([^<]+)</div>'
)


class RouterUnreachable(Exception):
    pass


def _get(url, timeout=10):
    return requests.get(
        url,
        auth=HTTPBasicAuth(config.DASHBOARD_AUTH_USER, config.DASHBOARD_AUTH_PASS),
        timeout=timeout,
    )


def fetch_dashboard_html():
    """Try the tunnel URL first (works from anywhere), then the LAN URL."""
    errors = []
    for url in (config.DASHBOARD_URL_TUNNEL, config.DASHBOARD_URL_LAN):
        if not url:
            continue
        try:
            resp = _get(url)
            resp.raise_for_status()
            return resp.text, url
        except Exception as e:
            errors.append(f"{url}: {e}")
    raise RouterUnreachable("; ".join(errors) or "no dashboard URL configured")


def parse_stats(html):
    stats = {}
    for label, value in STAT_RE.findall(html):
        stats[label.strip()] = value.strip()
    return stats


def get_current_stats():
    html, source_url = fetch_dashboard_html()
    stats = parse_stats(html)
    stats["_source_url"] = source_url
    return stats


if __name__ == "__main__":
    try:
        print(get_current_stats())
    except RouterUnreachable as e:
        print(f"Router unreachable: {e}")
