#!/bin/bash
set -x
set -euo pipefail

# Ensure CLOCK_TAI has the correct leap-second offset so that
# ktime_get_clocktai_ns() returns real TAI.  Without this, the
# mock PHC's extts timestamps are in UTC and ts2phc (which converts
# NMEA to TAI via the leapfile) sees a permanent ~37 s offset.
if ! python3 -c "
import ctypes, ctypes.util, os, time
# Match linux uapi timex (tai + 11 trailing 32-bit pads) so the kernel
# copy-out cannot overrun the ctypes allocation.
class Timex(ctypes.Structure):
    _fields_ = [('modes',ctypes.c_uint),('offset',ctypes.c_long),
        ('freq',ctypes.c_long),('maxerror',ctypes.c_long),
        ('esterror',ctypes.c_long),('status',ctypes.c_int),
        ('constant',ctypes.c_long),('precision',ctypes.c_long),
        ('tolerance',ctypes.c_long),('time_sec',ctypes.c_long),
        ('time_usec',ctypes.c_long),('tick',ctypes.c_long),
        ('ppsfreq',ctypes.c_long),('jitter',ctypes.c_long),
        ('shift',ctypes.c_int),('stabil',ctypes.c_long),
        ('jitcnt',ctypes.c_long),('calcnt',ctypes.c_long),
        ('errcnt',ctypes.c_long),('stbcnt',ctypes.c_long),
        ('tai',ctypes.c_int),('_padding',ctypes.c_int * 11)]
libc = ctypes.CDLL(ctypes.util.find_library('c') or 'libc.so.6', use_errno=True)
tx = Timex(modes=0x0080, constant=37)
if libc.adjtimex(ctypes.byref(tx)) == -1:
    e = ctypes.get_errno()
    raise OSError(e, os.strerror(e))
d = time.clock_gettime(11) - time.clock_gettime(0)
print(f'TAI offset set: {d:.0f}s')
"; then
    echo "ERROR: Failed to set CLOCK_TAI leap-second offset" >&2
    exit 1
fi

if [[ "${DKMS_MODE:-}" == "true" ]]; then
    modprobe -r netdevsim || true
    modprobe -r nsim_dpll || true
    modprobe -r nsim_ptp_mock || true
    modprobe -r nsim_ptp || true
    # Reload gnss module to reset the GNSS IDA minor allocator so new
    # devices start from gnss0 (gpsd/ts2phc expect /dev/gnss0).
    # Callers must stop gnss-sim / linuxptp-daemon (or any /dev/gnss*
    # opener) first; otherwise gnss stays busy and IDs resume at gnss1+.
    if ! modprobe -r gnss 2>/dev/null; then
        echo "WARNING: could not unload gnss (still in use?); /dev/gnss0 may be skipped" >&2
    fi
    modprobe gnss
    modprobe nsim_ptp
    modprobe nsim_dpll
    modprobe netdevsim pci_bus_nr=0x1f
    chmod 666 /dev/nsim_ptp* 2>/dev/null || true
    udevadm trigger --subsystem-match=gnss 2>/dev/null || true
    udevadm settle
else
    modprobe -r netdevsim
    # Reload gnss module to reset the GNSS IDA minor allocator so new
    # devices start from gnss0.
    modprobe -r gnss 2>/dev/null || true
    modprobe gnss
    modprobe netdevsim pci_bus_nr=0x1f
fi
# gpsd drops to nobody; without world/group access it fails with EACCES
# and frees /dev/gnss*, which starves ts2phc of NMEA.
chmod 666 /dev/gnss* 2>/dev/null || true
