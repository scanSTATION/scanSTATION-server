#!/bin/bash
#
# build-image.sh — build the self-contained scanSTATION image (CLI + REST
# server + button scanning + the drivers' firmware) with one command.
#
# Firmware is not taken from this repository: during the Docker build each
# selected driver's library obtains it from the manufacturer's distribution
# and verifies it (see lib-*/scanstation/). For v370: the vendor's website, then
# files you provide, then an installed Flatpak, then Flathub.
#
# Usage:
#   scripts/build-image.sh [--drivers "v370 ..."] [--bundle FILE] [--firmware FILE] [--flatpak] [--tag NAME]
#
#   --drivers LIST   drivers to include (default: v370; available: see
#                    lib-*/scanstation/drivers)
#   --bundle FILE    v370: use the vendor's bundle downloaded in a browser
#                    (lib-v370/scripts/install-firmware.sh --url prints the link)
#   --firmware FILE  v370: use an esfwdd.bin from elsewhere
#   --flatpak        v370: use the esfwdd.bin of the vendor app Flatpak on this host
#   --tag NAME       image name (default: scanstation:latest)
#
# Without options the build tries the vendor's website and then Flathub.
#
set -euo pipefail
cd "$(dirname "$0")/.."

TAG=scanstation:latest
DRIVERS=v370
SOURCES=firmware-sources
COPIED=()
cleanup() { [ ${#COPIED[@]} -eq 0 ] || rm -f "${COPIED[@]}"; }
trap cleanup EXIT
die() { echo "Error: $*" >&2; exit 1; }

# provide FILE NAME — copy FILE into the build's firmware sources as NAME.
provide() {
    [ -f "$1" ] || die "$1 not found"
    cp "$1" "$SOURCES/$2"
    COPIED+=("$SOURCES/$2")
    echo "firmware source: $1"
}

while [ $# -gt 0 ]; do
    case "$1" in
    --bundle) [ $# -ge 2 ] || die "--bundle needs a file"; provide "$2" "provided-$(basename "$2")"; shift 2 ;;
    --firmware) [ $# -ge 2 ] || die "--firmware needs a file"; mkdir -p "$SOURCES/provided"; provide "$2" provided/esfwdd.bin; shift 2 ;;
    --flatpak)
        loc=$(flatpak info --show-location net.epson.epsonscan2 2>/dev/null) || die "the vendor app Flatpak is not installed on this host"
        mkdir -p "$SOURCES/flatpak"
        provide "$loc/files/share/epsonscan2/esfwdd.bin" flatpak/esfwdd.bin
        shift ;;
    --tag) [ $# -ge 2 ] || die "--tag needs a name"; TAG=$2; shift 2 ;;
    --drivers) [ $# -ge 2 ] || die "--drivers needs a list"; DRIVERS=$2; shift 2 ;;
    -h | --help) sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option $1 (see --help)" ;;
    esac
done

command -v docker >/dev/null || die "docker not found"
[ -f lib-v370/go.mod ] || die "lib-v370 submodule missing: git submodule update --init"

docker build --build-arg DRIVERS="$DRIVERS" --target runtime -t "$TAG" .

cat <<EOF

Built $TAG with drivers: $DRIVERS. Run the REST server with button scanning
(scanner on USB):

  docker compose up -d server

  (no device path needed: the container sees /dev/bus/usb and finds the
  scanner by its USB ID, also after it was switched off and on)

  API:          http://localhost:8080
  Scans:        ./data/ (API scans and button scans)
  Buttons:      PDF → lossless A4 PDF; settings in ./config/config.json
  One-off scan: docker compose run --rm scan --dpi 300 --format png
EOF
