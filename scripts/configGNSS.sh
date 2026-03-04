#!/bin/bash
#
# configGNSS.sh — Sets up the GNSS simulator in the CI environment.
#
# Creates the gnss-sim container (socat PTY pairs + NMEA generator)
# and makes the virtual serial ports accessible to the Kind cluster
# workers via shared /dev mounts.
#
# Usage: ./configGNSS.sh <registry-ip>
#
set -x
set -euo pipefail

VM_IP=$1

GNSS_SIM_API_PORT="${GNSS_SIM_API_PORT:-9200}"

echo "=== Starting GNSS simulator container ==="

podman rm -f gnss-sim 2>/dev/null || true

# The gnss-sim container creates socat PTY pairs and writes NMEA at 1 Hz.
# /dev is volume-mounted so the PTY symlinks appear on the host (and in
# Kind nodes via their own /dev mount in kind-config.yaml).
podman run -d \
    --privileged \
    --volume /dev:/dev \
    --replace \
    --pull always \
    --name gnss-sim \
    -p "${GNSS_SIM_API_PORT}:${GNSS_SIM_API_PORT}" \
    -e "GNSS_SIM_API_PORT=${GNSS_SIM_API_PORT}" \
    -e "GNSS_PTY_TS2PHC=/dev/ttyGNSS_TS2PHC" \
    -e "GNSS_PTY_GNSS0=/dev/ttyGNSS_GNSS0" \
    "${VM_IP}/test:gnss-sim"

# Wait for the GNSS simulator to be ready
echo "Waiting for GNSS simulator to become healthy..."
retries=0
while [ $retries -lt 30 ]; do
    if curl -sf "http://localhost:${GNSS_SIM_API_PORT}/health" >/dev/null 2>&1; then
        echo "GNSS simulator is healthy"
        break
    fi
    sleep 1
    retries=$((retries + 1))
done

if [ $retries -ge 30 ]; then
    echo "ERROR: GNSS simulator did not become healthy after 30 seconds"
    podman logs gnss-sim
    exit 1
fi

# Verify PTY symlinks exist
for pty in /dev/ttyGNSS_TS2PHC /dev/ttyGNSS_GNSS0; do
    if [ ! -e "$pty" ]; then
        echo "ERROR: PTY symlink $pty not found on host"
        podman logs gnss-sim
        exit 1
    fi
    echo "PTY symlink $pty exists"
done

# Verify NMEA output
echo "Verifying NMEA output on /dev/ttyGNSS_GNSS0..."
NMEA_LINE=$(timeout 3 head -n 1 /dev/ttyGNSS_GNSS0 2>/dev/null || true)
if echo "$NMEA_LINE" | grep -q "GNRMC\|GNGGA\|GPZDA"; then
    echo "GNSS simulator producing valid NMEA: $NMEA_LINE"
else
    echo "WARNING: Could not read NMEA from /dev/ttyGNSS_GNSS0 (may need a moment to start)"
fi

echo "=== GNSS simulator setup complete ==="
echo "  API:          http://localhost:${GNSS_SIM_API_PORT}"
echo "  ts2phc PTY:   /dev/ttyGNSS_TS2PHC"
echo "  GNSS device:  /dev/ttyGNSS_GNSS0"
