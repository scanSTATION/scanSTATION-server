#!/bin/bash
#
# test-image.sh — integration test of the operator image (scripts/build-image.sh):
#   - the firmware inside the image is the vendor's verified file;
#   - scanstation-server (v370 driver) runs against lib-v370's synthetic
#     scanner in the power-on state and uploads the image's firmware file;
#   - a simulated PDF button press stores a PDF in the bind-mounted data dir;
#   - a REST API scan (fresh container) stores a PNG there.
#
# Usage: scripts/test-image.sh [--build]     (--build: build the image first)
#
set -euo pipefail
cd "$(dirname "$0")/.."
TAG=${IMAGE_TEST_TAG:-scanstation:latest}
FW_SHA256=2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954
[ "${1:-}" = --build ] && scripts/build-image.sh --tag "$TAG"

export IMAGE_TEST_DIR IMAGE_TEST_PORT IMAGE_TEST_TAG=$TAG IMAGE_TEST_UID IMAGE_TEST_GID IMAGE_TEST_BUTTONS=pdf
IMAGE_TEST_DIR=$(mktemp -d "$PWD/tmp/image-test.XXXXXX" 2>/dev/null || { mkdir -p tmp; mktemp -d "$PWD/tmp/image-test.XXXXXX"; })
IMAGE_TEST_PORT=${IMAGE_TEST_PORT:-18080}
IMAGE_TEST_UID=$(id -u) IMAGE_TEST_GID=$(id -g)
BASE=http://127.0.0.1:$IMAGE_TEST_PORT
COMPOSE=(docker compose -f docker-compose.test.yml --profile image)
fail=0
pass() { echo "PASS $*"; }
bad() { echo "FAIL $*"; fail=1; }
finish() {
    [ $fail -eq 0 ] || "${COMPOSE[@]}" logs image 2>/dev/null | tail -30
    "${COMPOSE[@]}" down >/dev/null 2>&1 || true
    rm -rf "$IMAGE_TEST_DIR"
}
trap finish EXIT

# 1. firmware inside the image
got=$(docker run --rm --entrypoint sha256sum "$TAG" /home/scan/firmware/esfwdd.bin | awk '{print $1}')
[ "$got" = "$FW_SHA256" ] && pass "image firmware sha256" || bad "image firmware sha256 $got"

# 2. button configuration: PDF → 100 dpi (small and fast)
mkdir -p "$IMAGE_TEST_DIR/data" "$IMAGE_TEST_DIR/config"
cat > "$IMAGE_TEST_DIR/config/config.json" <<'JSON'
{"buttons": {
  "pdf":     {"action": "scan", "format": "pdf", "dpi": 100, "size": "a4", "output_dir": "~/Documents", "prefix": "button_test"},
  "email":   {"action": "none"},
  "printer": {"action": "none"}
}}
JSON

# start — (re)create the server container and wait until it is healthy.
start() {
    "${COMPOSE[@]}" up -d --force-recreate image >/dev/null 2>&1
    for _ in $(seq 60); do curl -fsS "$BASE/api/health" >/dev/null 2>&1 && break; sleep 1; done
    curl -fsS "$BASE/api/health" >/dev/null && pass "server healthy ($1)" || bad "server not healthy ($1)"
}
start "buttons"

# 3. button scan (simulated PDF press)
for _ in $(seq 90); do [ -f "$IMAGE_TEST_DIR/data/button_test.pdf" ] && break; sleep 1; done
if [ -f "$IMAGE_TEST_DIR/data/button_test.pdf" ] && head -c 5 "$IMAGE_TEST_DIR/data/button_test.pdf" | grep -q '%PDF-'; then
    pass "PDF button scan stored in the data directory"
else
    bad "no button PDF in $IMAGE_TEST_DIR/data: $(ls "$IMAGE_TEST_DIR/data")"
fi
"${COMPOSE[@]}" logs image 2>&1 | grep -q "uploading firmware.*sha256=$FW_SHA256" && pass "image firmware uploaded to the (synthetic) scanner" || bad "no upload of the image firmware in the log"

# 4. API scan in a fresh container without button presses
IMAGE_TEST_BUTTONS=""
start "API"
code=$(curl -sS -o "$IMAGE_TEST_DIR/api.json" -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"dpi": 100, "format": "png", "prefix": "api_image"}' "$BASE/api/scan" || echo 000)
if [ "$code" = 200 ] && [ -f "$IMAGE_TEST_DIR/data/api_image.png" ]; then
    pass "API scan stored api_image.png"
else
    bad "API scan: HTTP $code $(cat "$IMAGE_TEST_DIR/api.json" 2>/dev/null)"
fi
exit $fail
