#!/bin/bash
set -x
set -euo pipefail

if [[ "${DKMS_MODE:-}" == "true" ]]; then
    modprobe -r netdevsim || true
    modprobe -r nsim_dpll || true
    modprobe -r nsim_ptp_mock || true
    modprobe -r nsim_ptp || true
    modprobe nsim_ptp
    modprobe nsim_dpll
    modprobe netdevsim pci_bus_nr=0x1f
    chmod 666 /dev/nsim_ptp* 2>/dev/null || true
else
    modprobe -r netdevsim
    modprobe netdevsim pci_bus_nr=0x1f
fi
modprobe openvswitch