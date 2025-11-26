#!/bin/bash
set -x
set -euo pipefail

modprobe -r netdevsim 
modprobe netdevsim pci_bus_nr=0x1f
modprobe openvswitch