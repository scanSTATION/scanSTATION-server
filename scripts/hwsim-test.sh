#!/bin/bash
#
# hwsim-test.sh — Tier-2 test with PRIVATE recordings: the real vendor app
# (driven by scanstation --driver epsonscan2) scans from the simulated
# scanner replaying a recording. Recordings are not part of the repository
# (they contain scans and the manufacturer's firmware upload).
# Thin wrapper around lib-v370/scripts/hwsim-test.sh (simulator, vhci attach,
# root handling — see there).
#
# Usage:
#   scripts/hwsim-test.sh TRACE [--native] [--disable-real]
#
#   TRACE   a recording, e.g. $SCANSTATION_TRACES/a4-300dpi-jpeg.jsonl.gz
#   --native  use /opt/epsonscan2 instead of the Flatpak
#
# Tests of the library itself (usbfs, protocol, v370 tool):
#   lib-v370/scripts/hwsim-test.sh TRACE --run TestDirectScan
#
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ $# -ge 1 ] || { sed -n '2,16p' "$0"; exit 2; }
TRACE="$(realpath "$1")"; shift
NATIVE=0
ARGS=()
for a in "$@"; do
    case "$a" in
        --native) NATIVE=1 ;;
        *) ARGS+=("$a") ;;
    esac
done
mkdir -p "$ROOT/bin"
(cd "$ROOT" && go build -tags "v370 epsonscan2" -o bin/scanstation ./cmd/scanstation)
export SCANSTATION_BIN="$ROOT/bin/scanstation" VENDORAPP_NATIVE="$NATIVE"
exec "$ROOT/lib-v370/scripts/hwsim-test.sh" "$TRACE" --pkg "$ROOT/integration/hwsim" --run TestReplayedScan "${ARGS[@]}"
