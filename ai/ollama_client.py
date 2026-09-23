"""Thin wrapper around the local Ollama chat API."""

import requests
import config


def chat(system_prompt, user_prompt, model=None, timeout=120):
    model = model or config.OLLAMA_MODEL
    resp = requests.post(
        f"{config.OLLAMA_HOST}/api/chat",
        json={
            "model": model,
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": user_prompt},
            ],
            "stream": False,
        },
        timeout=timeout,
    )
    resp.raise_for_status()
    return resp.json()["message"]["content"]
