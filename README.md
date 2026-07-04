# pixel-router

Turning a used Pixel 6 into a permanent, always-on 5G/4G hotspot router, plus tooling to monitor and display its status.

## Structure

- **`aod/`** — Android app implementing a custom always-on display (Daydream/DreamService) that mirrors the router's live status natively on the phone's lock screen: connection speed, connected devices, data usage, battery, thermal state, SMS-forwarder health, internet reachability, and signal quality. Includes automatic safety cutoffs (pauses itself above 45°C or below 30% battery).
- **`ai/`** — Local Ollama-based monitoring layer that polls the router's dashboard, keeps a rolling history, flags anomalies, answers natural-language questions about router health, and posts desktop alerts. Runs independently of the phone — the router itself stays a dumb, deterministic device with no AI dependency of its own.

## Setup

Both subprojects need real dashboard credentials that are **not** committed to this repo:

- `aod/dashboard.properties` — copy from `aod/dashboard.properties.example` and fill in your router dashboard's basic-auth username/password.
- `ai/local_secrets.py` — copy from `ai/local_secrets.py.example` and fill in the same credentials.

See each subproject's own files for further details on running/building.
