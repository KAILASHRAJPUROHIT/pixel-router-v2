"""
Main analysis loop. Runs forever: every POLL_INTERVAL_SECONDS, pulls
current router stats, appends to rolling history, and asks the local
Ollama model to review recent trends for anomalies / early-warning signs.
Fully best-effort - if the router isn't reachable this cycle (phone off
network, tunnel down, etc.) it just logs and waits for the next cycle.
Never touches or depends on the phone doing anything but serving its
existing dashboard; never calls out to any cloud AI service.
"""

import time
import traceback
from datetime import datetime, timezone

import config
import history
import notifier
import ollama_client
from fetch_router_data import get_current_stats, RouterUnreachable

SYSTEM_PROMPT = """You are a monitoring assistant for a home-built Android \
phone acting as an always-on 4G/5G hotspot router. You receive a snapshot \
of its current stats and a short history of recent snapshots. Your job:

1. Flag anything genuinely anomalous (rising temperature trend, signal \
degrading, SMS-forwarder down, device count dropping unexpectedly, data \
usage spiking abnormally).
2. If nothing is wrong, say so plainly and briefly - don't invent concerns.
3. Be concise. Respond in at most 3 sentences unless something is actually \
wrong, in which case explain what and why briefly.
4. Do not repeat raw numbers back verbatim; interpret them.
"""


def format_history_for_prompt(entries):
    lines = []
    for e in entries:
        s = e["stats"]
        lines.append(
            f"{e['timestamp']}: Down={s.get('Down')} Up={s.get('Up')} "
            f"Devices={s.get('Devices')} Total={s.get('Total Used')} "
            f"Thermal={s.get('Thermal')} SkinTemp={s.get('Skin Temp')} "
            f"SMSFwd={s.get('SMS Fwd')} Battery={s.get('Battery')}"
        )
    return "\n".join(lines)


def run_once():
    timestamp = datetime.now(timezone.utc).isoformat()
    try:
        stats = get_current_stats()
    except RouterUnreachable as e:
        print(f"[{timestamp}] router unreachable this cycle: {e}")
        return

    history.append_stats({"timestamp": timestamp, "stats": stats})
    recent = history.read_stats(limit=20)

    user_prompt = (
        "Recent router snapshots (oldest to newest):\n"
        f"{format_history_for_prompt(recent)}\n\n"
        "Review the trend and the latest snapshot. Is anything anomalous "
        "or worth flagging?"
    )

    try:
        analysis = ollama_client.chat(SYSTEM_PROMPT, user_prompt)
    except Exception as e:
        print(f"[{timestamp}] ollama call failed: {e}")
        return

    insight = {"timestamp": timestamp, "stats": stats, "analysis": analysis}
    history.append_insight(insight)
    print(f"[{timestamp}] insight: {analysis}")

    lowered = analysis.lower()
    if any(w in lowered for w in ("warn", "anomal", "flag", "issue", "problem", "down", "degrad")):
        # A soft heuristic gate so routine "all clear" summaries don't spam
        # a desktop toast every 15 minutes - only surface when the model's
        # own wording suggests something is actually worth a look.
        if "all clear" not in lowered and "nothing wrong" not in lowered and "no anomal" not in lowered:
            notifier.alert("Router AI Alert", analysis[:250])


def main():
    print(f"Starting router AI analyzer - polling every {config.POLL_INTERVAL_SECONDS}s")
    while True:
        try:
            run_once()
        except Exception:
            traceback.print_exc()
        time.sleep(config.POLL_INTERVAL_SECONDS)


if __name__ == "__main__":
    main()
