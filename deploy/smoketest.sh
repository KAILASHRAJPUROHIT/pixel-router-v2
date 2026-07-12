#!/usr/bin/env bash
# Automated post-deploy verification - encodes the exact manual checks
# (curl /internal/aod-stats, confirm all services running, confirm
# billing_day stuck) that were reconstructed by hand after every deploy
# throughout 2026-07-11's session. Exits non-zero on any failure so
# push.sh (or a human) can tell a deploy actually succeeded rather than
# just "the commands didn't error."
set -uo pipefail
cd "$(dirname "$0")/.."

# MSYS_NO_PATHCONV=1 is needed for adb (a non-MSYS binary) to receive
# REMOTE device paths (e.g. /data/local/tmp/x) literally instead of
# MSYS rewriting them into a local Windows path first. But confirmed
# live 2026-07-12 that setting it globally then breaks curl.exe's own
# LOCAL -o file writes (exit 23) - curl doesn't understand a raw
# "/d/..." POSIX-style path as a Windows path, and MSYS_NO_PATHCONV
# stops MSYS from translating it for curl the way it normally would.
# Fix: scope it to each individual `adb` call (see adb() below) rather
# than exporting it for the whole script.
adb() { MSYS_NO_PATHCONV=1 command adb "$@"; }

TMPDIR_LOCAL="$(pwd)/deploy/.smoketest_tmp"
mkdir -p "$TMPDIR_LOCAL"

FAIL=0
PORT=19090

cleanup() {
  adb forward --remove "tcp:$PORT" >/dev/null 2>&1 || true
  rm -rf "$TMPDIR_LOCAL"
}
trap cleanup EXIT

adb forward "tcp:$PORT" tcp:9090 >/dev/null

echo "== supervisor_status.json: all services running? =="
STATUS_JSON=$(adb shell "su -c 'cat /data/adb/router_v2/state/supervisor_status.json'" 2>/dev/null)
TOTAL=$(echo "$STATUS_JSON" | grep -c '"state"')
RUNNING=$(echo "$STATUS_JSON" | grep -o '"state": "running"' | wc -l)
if [ "$TOTAL" -eq 0 ]; then
  echo "FAIL: could not read supervisor_status.json"
  FAIL=1
elif [ "$RUNNING" -ne "$TOTAL" ]; then
  echo "FAIL: only $RUNNING/$TOTAL services report running"
  echo "$STATUS_JSON" | grep -E '"name"|"state"|"restart_count"'
  FAIL=1
else
  echo "OK: all $TOTAL services running"
fi

echo "== /healthz =="
set +o pipefail
HEALTHZ_CODE=$(curl -s -o $TMPDIR_LOCAL/healthz.json -w "%{http_code}" --max-time 10 "http://127.0.0.1:$PORT/healthz")
CURL_EXIT=$?
set -o pipefail
if [ "$CURL_EXIT" -ne 0 ]; then
  echo "FAIL: curl itself failed (exit $CURL_EXIT) hitting /healthz"
  FAIL=1
elif [ "$HEALTHZ_CODE" != "200" ]; then
  echo "FAIL: /healthz returned HTTP $HEALTHZ_CODE"
  cat $TMPDIR_LOCAL/healthz.json 2>/dev/null
  FAIL=1
else
  echo "OK: /healthz returned 200"
fi

echo "== /internal/aod-stats (must be fast and complete) =="
set +o pipefail
AOD_CODE=$(curl -s -o $TMPDIR_LOCAL/aod.json -w "%{http_code}" --max-time 10 "http://127.0.0.1:$PORT/internal/aod-stats")
CURL_EXIT=$?
set -o pipefail
if [ "$CURL_EXIT" -ne 0 ]; then
  echo "FAIL: curl itself failed (exit $CURL_EXIT) hitting /internal/aod-stats"
  FAIL=1
elif [ "$AOD_CODE" != "200" ]; then
  echo "FAIL: /internal/aod-stats returned HTTP $AOD_CODE"
  FAIL=1
else
  for label in Down Up Devices "Total Used" Battery Thermal "Skin Temp" "SMS Fwd" Internet Signal; do
    if ! grep -q "\"label\":\"$label\"" $TMPDIR_LOCAL/aod.json; then
      echo "FAIL: aod-stats missing expected stat: $label"
      FAIL=1
    fi
  done
  [ "$FAIL" -eq 0 ] && echo "OK: aod-stats returned all 10 expected stats"
fi

echo "== billing_day (quota config) =="
adb shell "su -c 'cp /data/adb/router_v2/state/router.db /data/local/tmp/smoketest_router.db 2>/dev/null; chmod 666 /data/local/tmp/smoketest_router.db'" >/dev/null 2>&1
adb pull /data/local/tmp/smoketest_router.db "$TMPDIR_LOCAL/router.db" >/dev/null 2>&1
adb shell "rm -f /data/local/tmp/smoketest_router.db" >/dev/null 2>&1
if command -v sqlite3 >/dev/null 2>&1 && [ -f "$TMPDIR_LOCAL/router.db" ]; then
  BILLING_DAY=$(sqlite3 "$TMPDIR_LOCAL/router.db" "SELECT billing_day FROM billing_cycle LIMIT 1;" 2>/dev/null || echo "")
  if [ -z "$BILLING_DAY" ]; then
    echo "WARN: could not read billing_day (non-fatal)"
  else
    echo "OK: billing_day=$BILLING_DAY"
  fi
else
  echo "SKIP: no local sqlite3 available to check billing_day"
fi

rm -f $TMPDIR_LOCAL/healthz.json $TMPDIR_LOCAL/aod.json

if [ "$FAIL" -ne 0 ]; then
  echo
  echo "SMOKETEST FAILED"
  exit 1
fi
echo
echo "SMOKETEST PASSED"
