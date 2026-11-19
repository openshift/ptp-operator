#!/bin/bash
# Smoke-test the VRT shim against a mock PHC (requires loaded netdevsim-dkms).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
make -C "$ROOT" all

NSIM_NEW=/sys/bus/netdevsim/new_device
NSIM_DEL=/sys/bus/netdevsim/del_device
ID=99
ARCH=$(uname -m)
BUS_NR=$(cat /sys/module/netdevsim/parameters/pci_bus_nr 2>/dev/null || echo 31)
BUS=$(printf "%02x" "$BUS_NR")
if [[ "$ARCH" == "x86_64" ]]; then
	PCI="0000:${BUS}:1f.0"
elif [[ "$ARCH" == "aarch64" ]]; then
	PCI="0001:${BUS}:1f.0"
else
	echo "skip: unsupported arch $ARCH"
	exit 0
fi

if [[ ! -w "$NSIM_NEW" ]]; then
	echo "skip: netdevsim not available (need root + modules)"
	exit 0
fi

cleanup() { echo "$ID" >"$NSIM_DEL" 2>/dev/null || true; }
trap cleanup EXIT

echo "$ID" >"$NSIM_DEL" 2>/dev/null || true
echo "$ID $PCI $ID 1" >"$NSIM_NEW"
udevadm settle 2>/dev/null || sleep 0.5

IFACE=""
for netdir in "/sys/bus/pci/devices/${PCI}/net" \
	"/sys/bus/netdevsim/devices/netdevsim${ID}/net"; do
	[[ -d "$netdir" ]] || continue
	IFACE=$(find "$netdir" -maxdepth 1 -mindepth 1 -type d -printf '%f\n' | head -1)
	[[ -n "$IFACE" ]] && break
done
[[ -n "$IFACE" ]]
PHC=$(ethtool -T "$IFACE" | awk '/PTP Hardware Clock:/ {print $4}')
DEV="/dev/ptp${PHC}"
[[ -e "$DEV" ]]
# Restrict to owner/group; this smoke test runs as root.
chmod 660 "$DEV" 2>/dev/null || true

export PTP_VRT_PHC="$DEV"
export LD_PRELOAD="$ROOT/libptp_vrt_shim.so"

# Host CLOCK_REALTIME must be read without the shim (env -u LD_PRELOAD).
HOST_BEFORE=$(env -u LD_PRELOAD date +%s.%N)
BEFORE=$("$ROOT/test_vrt_cli" gettime)
"$ROOT/test_vrt_cli" step >/dev/null
AFTER=$("$ROOT/test_vrt_cli" gettime)
HOST_AFTER=$(env -u LD_PRELOAD date +%s.%N)

echo "vrt_before=$BEFORE vrt_after=$AFTER host_before=$HOST_BEFORE host_after=$HOST_AFTER"
python3 - <<PY
b=float("$BEFORE"); a=float("$AFTER")
hb=float("$HOST_BEFORE"); ha=float("$HOST_AFTER")
# CLI steps the stand-in PHC by +1s; allow small runtime jitter.
assert abs((a - b) - 1.0) < 0.25, (b, a, a - b)
# Unshimmed host realtime must not follow the VRT step.
assert abs(ha - hb) < 0.5, (hb, ha, ha - hb)
assert abs(b - hb) < 120, (b, hb)  # stand-in starts near realtime
print("OK: shim steers stand-in PHC", "$DEV")
PY
