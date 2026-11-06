# phc2sys virtual CLOCK_REALTIME shim (CI only)

Kind workers share one host `CLOCK_REALTIME`. This LD_PRELOAD library makes
`phc2sys -a -r` discipline a **per-node stand-in mock PHC** instead, while
keeping log lines as `CLOCK_REALTIME` so cloud-event-proxy metrics/events work.

## Components

| File | Role |
|------|------|
| `ptp_vrt_shim.c` | Redirects `clock_gettime/settime/adjtime(CLOCK_REALTIME)` and `adjtimex` to the stand-in PHC; fakes `PTP_SYS_OFFSET*` against that PHC |
| `phc2sys-wrapper` | Installed as `/usr/sbin/phc2sys`; enables preload when `/var/run/vrt/device` exists |
| `../create-vrt-clocks.sh` | Creates one netdevsim mock PHC per Kind worker and writes the device path into each node |

## Activation

1. Build/load netdevsim-dkms modules.
2. Bring up Kind + topology (`configServers.sh`).
3. Run `create-vrt-clocks.sh` (writes `/var/run/ptp/vrt/device` per node → pod path `/var/run/vrt/device`).
4. linuxptp image must contain the shim + wrapper (see `../../ptp-tools/Dockerfile.lptpd`).
5. Tests: `run-tests.sh` sets `DisableAllSlaveRTUpdate: false` when `DKMS_MODE=true`
   or when `/var/run/vrt/device` already exists on the cluster (prior VRT install).

This is **not** a second kernel realtime clock. Host `date` is unchanged.
