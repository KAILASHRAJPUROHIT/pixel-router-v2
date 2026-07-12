#!/usr/bin/env bash
# One-command V2 deploy: pushes every built binary + services.json,
# then runs remote_deploy.sh on-device to move them into place and
# restart safely. Replaces the hand-typed, error-prone adb sequences
# used throughout development (which caused at least one real
# orphaned-process incident on 2026-07-11) with a single reviewable,
# repeatable script.
#
# Requires deploy/build.sh to have been run first (this script does
# NOT build - keeping build and deploy separate means a build never
# silently becomes a live deploy).
set -euo pipefail
cd "$(dirname "$0")/.."

# MSYS/Git-Bash on Windows otherwise rewrites absolute-looking remote
# paths (e.g. /data/local/tmp/x) into a local Windows path before
# handing them to adb, which is not an MSYS-aware program - confirmed
# live 2026-07-11 as the cause of repeated "remote secure_mkdirs()
# failed" errors. Scoped to a wrapper function (not a global export)
# since exporting it would otherwise leak into smoketest.sh's own
# environment when this script calls it below and break ITS curl calls
# - confirmed live 2026-07-12 (see smoketest.sh's own comment on this).
adb() { MSYS_NO_PATHCONV=1 command adb "$@"; }

OUT=deploy/out
if [ ! -d "$OUT" ] || [ -z "$(ls -A "$OUT" 2>/dev/null)" ]; then
  echo "no built binaries found in $OUT - run deploy/build.sh first" >&2
  exit 1
fi

echo "== pushing binaries to /data/local/tmp =="
for f in "$OUT"/*; do
  [ -f "$f" ] || continue
  adb push "$f" "/data/local/tmp/$(basename "$f")"
done

echo "== pushing services.json =="
adb push deploy/services.json /data/local/tmp/services.json

echo "== pushing and running remote_deploy.sh =="
adb push deploy/remote_deploy.sh /data/local/tmp/remote_deploy.sh
adb shell "su -c 'sh /data/local/tmp/remote_deploy.sh'"

echo "== running smoketest =="
bash "$(dirname "$0")/smoketest.sh"
