# Dockerfile — scanSTATION server
#
# Build argument DRIVERS selects the scanner drivers (and thereby the
# libraries and firmware) in the image, e.g.
#   docker build --build-arg DRIVERS="v370" .              (default)
#   docker build --build-arg DRIVERS="v370 epsonscan2" .
# Each driver is listed in a library submodule's scanstation/drivers file
# (lib-*/scanstation/drivers), which also names its firmware script.
#
# Stages:
#   firmware   runs the firmware script of every selected driver
#   builder    compiles the binaries with the drivers as build tags
#   test       unit tests
#   testsim    server against the vendor app's stand-in (integration tests)
#   runtime    the production image (default target, must stay last)
#
# USB: the containers need the live /dev/bus/usb tree and access to USB
# character devices (see docker-compose.yml); no privileged mode.
ARG DRIVERS="v370"

# --- firmware stage ------------------------------------------------------
# Firmware is never taken from a repository: each driver's script obtains it
# from the manufacturer's distribution and verifies it. Optional local
# sources (e.g. a bundle downloaded in a browser) go into firmware-sources/.
FROM debian:bookworm-slim AS firmware
ARG DRIVERS
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates curl libarchive-tools xz-utils zstd flatpak \
    && rm -rf /var/lib/apt/lists/*
COPY firmware-sources/ /sources/
COPY lib-v370/scanstation/ /libs/lib-v370/scanstation/
COPY lib-v370/scripts/ /libs/lib-v370/scripts/
RUN set -e; mkdir -p /fw/firmware; \
    for d in $DRIVERS; do \
        found=; \
        for lib in /libs/*; do \
            script=$(awk -v d="$d" '$1 == d { print $2 }' "$lib/scanstation/drivers"); \
            [ -n "$script" ] || continue; \
            found=1; \
            if [ "$script" != "-" ]; then \
                echo "== firmware for driver $d ($lib/$script)"; \
                FW_SOURCES=/sources "$lib/$script" /fw; \
            fi; \
        done; \
        [ -n "$found" ] || { echo "no library provides driver $d" >&2; exit 1; }; \
    done; \
    ls -l /fw/firmware

# --- builder stage -------------------------------------------------------
FROM golang:1.26-bookworm AS builder
ARG DRIVERS
WORKDIR /src
COPY go.mod go.sum* ./
COPY lib-v370/go.mod lib-v370/go.sum* lib-v370/
RUN go mod download 2>/dev/null || true
COPY . .
RUN set -e; for cmd in scanstation scanstation-server; do \
        CGO_ENABLED=0 go build -tags "$DRIVERS" -trimpath -ldflags="-s -w" -o /out/$cmd ./cmd/$cmd; \
    done; \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/vendorapp-fake github.com/scanSTATION/lib-v370/cmd/vendorapp-fake

# --- test stage ----------------------------------------------------------
FROM builder AS test
ARG DRIVERS
ENV DRIVERS=${DRIVERS}
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils \
    && rm -rf /var/lib/apt/lists/*
CMD ["sh", "-c", "go test -race -count=1 -tags \"$DRIVERS\" ./..."]

# --- shared runtime base -------------------------------------------------
FROM debian:bookworm-slim AS runtime-base
# poppler-utils: pdfunite for multi-page PDFs.
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/scanstation /usr/local/bin/scanstation
COPY --from=builder /out/scanstation-server /usr/local/bin/scanstation-server
RUN (getent group scanner >/dev/null || groupadd -r scanner) && \
    useradd -r -g scanner -d /home/scan -m scan
# Drivers load their firmware from ./firmware/ relative to the working
# directory. HOME is /home/scan for every user: the button configuration is
# /home/scan/.config/scanstation/config.json, button scans go to
# /home/scan/Documents. Temporary files go to /tmp (runs as any user).
ENV HOME=/home/scan XDG_CACHE_HOME=/tmp/cache
WORKDIR /home/scan
COPY --from=firmware /fw/firmware/ /home/scan/firmware/
RUN mkdir -p /home/scan/Documents /home/scan/.config/scanstation /output \
    && chown -R scan:scanner /home/scan /output
VOLUME ["/output"]
EXPOSE 8080

# --- integration-test image: real server, the vendor app stand-in ----------
FROM runtime-base AS testsim
COPY --from=builder /out/vendorapp-fake /usr/local/bin/vendorapp-fake
ENTRYPOINT ["scanstation-server"]
CMD ["--addr", ":8080", "-o", "/output", "--driver", "epsonscan2", "--epsonscan2-native", "--epsonscan2-native-path", "/usr/local/bin/vendorapp-fake"]

# --- runtime stage (default) ---------------------------------------------
FROM runtime-base AS runtime
ENTRYPOINT ["scanstation"]
CMD ["--help"]
