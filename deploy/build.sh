#!/usr/bin/env bash
# Cross-compiles every V2 binary for the device and stages them under
# deploy/out/ - run from the repo root. Deliberately does not touch
# /data/adb/router_v2 on the device itself; pushing is a separate,
# explicit step (see deploy/README.md) so a build never silently
# becomes a live deploy.
set -euo pipefail
cd "$(dirname "$0")/.."

export GOOS=linux GOARCH=arm64 CGO_ENABLED=0
OUT=deploy/out
mkdir -p "$OUT"

# Build-time version stamp (see internal/buildinfo) - this repo has no
# .git to derive a real commit SHA from, so a UTC build timestamp is
# the next best "is this the binary I think it is" signal, surfaced at
# supervisor startup and in webserver's /healthz.
VERSION=$(date -u +%Y%m%d-%H%M%S)
LDFLAGS="-X github.com/antriva/pixel-router-v2/internal/buildinfo.Version=$VERSION"
echo "version: $VERSION"

for cmd in supervisor webserver webproxy hotspot usbtether forwarder quota tunnel cftunnel ttlproxy setup dreamwatchdog failover dbbackup devicescan thermalguard connmonitor logrotate qos vpntether powergovernor; do
  echo "building $cmd..."
  go build -ldflags "$LDFLAGS" -o "$OUT/$cmd" "./cmd/$cmd"
done

echo "done - binaries in $OUT/"
