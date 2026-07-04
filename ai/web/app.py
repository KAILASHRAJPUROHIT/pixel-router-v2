"""
Local-only web UI (127.0.0.1) showing the router AI's latest insights and
a Q&A box that asks fresh questions against current stats + recent
history via the local Ollama model. Not exposed to the internet - this
runs alongside analyzer.py on the RTX 4080 machine, for your own use.
"""

import sys
import os

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from flask import Flask, render_template, request, jsonify

import config
import history
import ollama_client
from fetch_router_data import get_current_stats, RouterUnreachable
from analyzer import format_history_for_prompt

app = Flask(__name__)

QA_SYSTEM_PROMPT = """You are a monitoring assistant for a home-built \
Android phone acting as an always-on 4G/5G hotspot router. Answer the \
user's question using the provided current stats and recent history. Be \
concise and specific. If the data doesn't contain enough information to \
answer confidently, say so rather than guessing."""


@app.route("/")
def index():
    insights = list(reversed(history.read_insights(limit=30)))
    try:
        current = get_current_stats()
        router_error = None
    except RouterUnreachable as e:
        current = None
        router_error = str(e)
    return render_template(
        "index.html", insights=insights, current=current, router_error=router_error
    )


@app.route("/ask", methods=["POST"])
def ask():
    question = request.form.get("question", "").strip()
    if not question:
        return jsonify({"answer": "Ask something first."})

    recent = history.read_stats(limit=20)
    try:
        current = get_current_stats()
        current_line = f"Current snapshot: {current}\n\n"
    except RouterUnreachable:
        current_line = "Current snapshot: unavailable (router unreachable right now)\n\n"

    user_prompt = (
        current_line
        + "Recent history:\n"
        + format_history_for_prompt(recent)
        + f"\n\nQuestion: {question}"
    )

    try:
        answer = ollama_client.chat(QA_SYSTEM_PROMPT, user_prompt)
    except Exception as e:
        answer = f"Couldn't reach the local AI model: {e}"

    return jsonify({"answer": answer})


if __name__ == "__main__":
    app.run(host=config.WEB_HOST, port=config.WEB_PORT, debug=False)
