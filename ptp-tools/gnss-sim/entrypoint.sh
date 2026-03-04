#!/bin/bash
# entrypoint.sh — Creates socat PTY pairs and starts the GNSS simulator.
#
# The simulator writes NMEA sentences to the writer-side of each PTY pair.
# Consumer processes (ts2phc, gpsd, test validation) read from the reader-side
# symlinks.
#
# Environment variables:
#   GNSS_SIM_API_PORT  — HTTP API port (default: 9200)
#   GNSS_PTY_TS2PHC   — reader-side symlink for ts2phc (default: /dev/ttyGNSS_TS2PHC)
#   GNSS_PTY_GNSS0    — reader-side symlink for gpsd/test (default: /dev/ttyGNSS_GNSS0)
#
# The two PTY endpoints serve different consumers:
#   /dev/ttyGNSS_TS2PHC — ts2phc reads from here (ts2phc.nmea_serialport)
#   /dev/ttyGNSS_GNSS0  — gpsd and test validation read from here (/dev/gnss0 equivalent)

set -euo pipefail

API_PORT="${GNSS_SIM_API_PORT:-9200}"
PTY_TS2PHC="${GNSS_PTY_TS2PHC:-/dev/ttyGNSS_TS2PHC}"
PTY_GNSS0="${GNSS_PTY_GNSS0:-/dev/ttyGNSS_GNSS0}"

WRITE_TS2PHC="/tmp/gnss_write_ts2phc"
WRITE_GNSS0="/tmp/gnss_write_gnss0"

cleanup() {
    echo "Cleaning up socat processes..."
    kill "${SOCAT_PID1:-}" "${SOCAT_PID2:-}" 2>/dev/null || true
    wait "${SOCAT_PID1:-}" "${SOCAT_PID2:-}" 2>/dev/null || true
}
trap cleanup EXIT

echo "Starting socat PTY pair for ts2phc: ${PTY_TS2PHC} <-> ${WRITE_TS2PHC}"
socat -d PTY,link="${PTY_TS2PHC}",raw,echo=0,mode=666 \
         PTY,link="${WRITE_TS2PHC}",raw,echo=0,mode=666 &
SOCAT_PID1=$!

echo "Starting socat PTY pair for gnss0: ${PTY_GNSS0} <-> ${WRITE_GNSS0}"
socat -d PTY,link="${PTY_GNSS0}",raw,echo=0,mode=666 \
         PTY,link="${WRITE_GNSS0}",raw,echo=0,mode=666 &
SOCAT_PID2=$!

# Wait for PTY symlinks to appear
for pty in "${WRITE_TS2PHC}" "${WRITE_GNSS0}" "${PTY_TS2PHC}" "${PTY_GNSS0}"; do
    retries=0
    while [ ! -e "${pty}" ] && [ $retries -lt 50 ]; do
        sleep 0.1
        retries=$((retries + 1))
    done
    if [ ! -e "${pty}" ]; then
        echo "ERROR: PTY ${pty} did not appear after 5 seconds"
        exit 1
    fi
done
echo "All PTY symlinks ready"

echo "Starting GNSS simulator (API port ${API_PORT})"
exec /usr/local/bin/gnss-sim \
    --outputs "${WRITE_TS2PHC},${WRITE_GNSS0}" \
    --api-port "${API_PORT}" \
    "$@"
