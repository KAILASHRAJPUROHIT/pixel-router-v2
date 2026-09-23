"""Rolling jsonl history store for router stats + AI insights."""

import json
import os
import config


def _append(path, entry, max_entries):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    lines = []
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as f:
            lines = f.readlines()
    lines.append(json.dumps(entry) + "\n")
    lines = lines[-max_entries:]
    with open(path, "w", encoding="utf-8") as f:
        f.writelines(lines)


def _read_all(path):
    if not os.path.exists(path):
        return []
    with open(path, "r", encoding="utf-8") as f:
        return [json.loads(line) for line in f if line.strip()]


def append_stats(entry):
    _append(config.HISTORY_FILE, entry, config.HISTORY_MAX_ENTRIES)


def read_stats(limit=None):
    entries = _read_all(config.HISTORY_FILE)
    return entries[-limit:] if limit else entries


def append_insight(entry):
    _append(config.INSIGHTS_FILE, entry, config.INSIGHTS_MAX_ENTRIES)


def read_insights(limit=None):
    entries = _read_all(config.INSIGHTS_FILE)
    return entries[-limit:] if limit else entries
