#!/bin/bash
#
# scan.sh — Quick wrapper to trigger a scan on the V370.
#
# Usage:
#   ./scan.sh                    # 300 DPI, A4, color, JPEG → ~/Documents/scan*.jpg
#   ./scan.sh 600                # 600 DPI
#   ./scan.sh 300 ~/Pictures     # custom output directory
#   ./scan.sh 300 jpeg 100       # JPEG at quality 100
#   ./scan.sh --multi            # multi-page PDF (interactive, press n/e)
#
# For multi-page scanning:
#   go run -tags "v370 epsonscan2" ./cmd/scanstation --multi --quality 100 --timestamp
#
set -euo pipefail

DPI="${1:-300}"
OUTPUT_DIR="${2:-$HOME/Documents}"
FORMAT="${3:-jpeg}"
QUALITY="${4:-85}"

# Ensure output directory exists
mkdir -p "${OUTPUT_DIR}"

# Build and run the Go CLI
cd "$(dirname "$0")"
go run -tags "v370 epsonscan2" ./cmd/scanstation --dpi "${DPI}" --format "${FORMAT}" --quality "${QUALITY}" --size a4 --color rgb -o "${OUTPUT_DIR}" --prefix scan
