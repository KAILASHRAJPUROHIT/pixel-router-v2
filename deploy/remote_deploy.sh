#!/system/bin/sh
# Runs ON THE DEVICE as root - the actual move-into-place + safe
# restart sequence for a V2 deploy. Pushed to /data/local/tmp and
# invoked by push.sh; encodes exactly the manual sequence used
# throughout 2026-07-11's session (capture old pids before touching
# anything, move binaries, stop old supervisor, kill its orphaned
# children by exact pid - SIGTERM to the supervisor does NOT kill
# already-running children, confirmed live earlier that session -
# verify ports are free, launch fresh, poll until every service
# reports running) so it never again has to be reconstructed by hand.
set -e

BASE=/data/adb/router_v2
BIN=$BASE/bin
STATE=$BASE/state
PIDFILE=$STATE/supervisor.pid
STATUS=$STATE/supervisor_status.json

echo "== capturing current child pids (if any) =="
OLD_PIDS=""
if [ -f "$STATUS" ]; then
  OLD_PIDS=$(grep -o '"pid": [0-9]*' "$STATUS" | awk '{print $2}')
fi
OLD_SUP_PID=""
if [ -f "$PIDFILE" ]; then
  OLD_SUP_PID=$(cat "$PIDFILE" 2>/dev/null)
fi
if [ -z "$OLD_SUP_PID" ] || ! kill -0 "$OLD_SUP_PID" 2>/dev/null; then
  # No pidfile, or a stale one - fall back to a direct process lookup.
  # Confirmed live 2026-07-11 that skipping this fallback lets an old
  # supervisor that predates the pidfile feature (or one whose pidfile
  # was lost some other way) keep running untouched: only its children
  # got killed, and being restart:always it just relaunched a fresh
  # child from the newly-moved binary itself, re-grabbing the port
  # before this script's own restart could bind it.
  OLD_SUP_PID=$(ps -ef | grep '[b]in/supervisor' | awk '{print $2}' | head -1)
fi

echo "== moving new binaries into place =="
for f in /data/local/tmp/*; do
  [ -f "$f" ] || continue
  name=$(basename "$f")
  case "$name" in
    services.json)
      mv "$f" "$BASE/services.json"
      chmod 644 "$BASE/services.json"
      ;;
    remote_deploy.sh) ;; # this script itself - not a binary to install
    *)
      mv "$f" "$BIN/$name"
      chmod 755 "$BIN/$name"
      ;;
  esac
done

if [ -n "$OLD_SUP_PID" ] && kill -0 "$OLD_SUP_PID" 2>/dev/null; then
  echo "== stopping old supervisor (pid $OLD_SUP_PID) =="
  kill "$OLD_SUP_PID" 2>/dev/null || true
  sleep 2
fi

if [ -n "$OLD_PIDS" ]; then
  echo "== killing old children by exact pid: $OLD_PIDS =="
  kill -9 $OLD_PIDS 2>/dev/null || true
  sleep 1
fi

# The tunnel service's own child (ngrok) is a GRANDCHILD of the old
# supervisor, not one of the direct children captured above from
# supervisor_status.json's "pid" field - confirmed live 2026-07-12 that
# it survives as an orphan (reparented to init) every time the tunnel
# service itself gets killed here, then holds the reserved domain
# online and blocks the freshly-launched tunnel's own ngrok from
# starting (ERR_NGROK_334) until someone notices and kills it by hand.
echo "== killing any orphaned ngrok/cloudflared process =="
NGROK_PIDS=$(ps -ef | grep '[n]grok http' | awk '{print $2}')
if [ -n "$NGROK_PIDS" ]; then
  kill -9 $NGROK_PIDS 2>/dev/null || true
fi
CF_PIDS=$(ps -ef | grep '[c]loudflared tunnel' | awk '{print $2}')
if [ -n "$CF_PIDS" ]; then
  kill -9 $CF_PIDS 2>/dev/null || true
fi

echo "== verifying ports are free =="
# `-l` alone isn't reliably a LISTEN-only filter on this device's busybox
# netstat - confirmed live 2026-07-12 that harmless closing connections
# (FIN_WAIT2) sharing the same port number were matching this check and
# aborting a perfectly safe restart. Explicitly requiring "LISTEN" in the
# matched line is what actually restricts this to a real bound listener.
if netstat -tlnp 2>/dev/null | grep -E ':9090 |:9091 |:12346 ' | grep -q LISTEN; then
  echo "ERROR: port 9090, 9091, or 12346 still bound after cleanup - aborting restart, investigate manually" >&2
  netstat -tlnp 2>/dev/null | grep -E ':9090 |:9091 |:12346 ' >&2
  exit 1
fi

echo "== launching fresh supervisor =="
nohup "$BIN/supervisor" \
  --manifest="$BASE/services.json" \
  --status="$STATUS" \
  --log="$BASE/logs/supervisor.jsonl" \
  --service-log-dir="$BASE/logs" \
  --alert-number=+918767635484 \
  --pidfile="$PIDFILE" \
  > "$BASE/logs/supervisor_stdout.log" 2>&1 < /dev/null &

echo "== waiting for all services to report running (up to 60s) =="
for i in $(seq 1 30); do
  sleep 2
  TOTAL=$(grep -c '"state"' "$STATUS" 2>/dev/null || echo 0)
  RUNNING=$(grep -o '"state": "running"' "$STATUS" 2>/dev/null | wc -l)
  if [ "$TOTAL" -gt 0 ] && [ "$RUNNING" -eq "$TOTAL" ]; then
    echo "all $TOTAL services running"
    exit 0
  fi
done

echo "WARNING: not all services reported running within 60s - current status:" >&2
grep -E '"name"|"state"|"restart_count"' "$STATUS" 2>/dev/null >&2
exit 1
