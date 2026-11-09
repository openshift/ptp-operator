#!/bin/bash
set -x
set -euo pipefail

# Configure netdevsim interface pairs connected with direct links.
# The servers (kind worker containers) are connected directly to each other,
# without any switch in between. PTP frames are exchanged over the direct
# links only, so every clock pair that needs to communicate (GM to slave,
# BC master port to downstream slave, ...) must share a link.
#
# Topology (see test/README.md):
#   server 1 (kind-netdevsim-worker):   nic1 = ens1f0, ens1f1  nic2 = ens2f0, ens2f1
#   server 2 (kind-netdevsim-worker2):  nic1 = ens3f0, ens3f1  nic2 = ens4f0, ens4f1
#   server 3 (kind-netdevsim-worker3):  nic1 = ens5f0  nic2 = ens6f0  nic3 = ens7f0, ens7f1
#
# Links:
#   server1.ens1f0 <--> server2.ens3f0  (GM <-> BC1 slave / OC slave)
#   server1.ens2f0 <--> server2.ens3f1  (GM <-> BC2 slave)
#   server1.ens1f1 <--> server3.ens5f0  (BC1 master <-> downstream slave 1)
#   server1.ens2f1 <--> server3.ens6f0  (BC2 master <-> downstream slave 2)
#   server2.ens4f0 <--> server3.ens7f0  (GM <-> dual follower port 0)
#   server2.ens4f1 <--> server3.ens7f1  (GM <-> dual follower port 1)

# Detect platform architecture and set PCI address prefix
ARCH=$(uname -m)
if [[ "$ARCH" == "x86_64" ]]; then
    PCI_PREFIX="0000:1f"
elif [[ "$ARCH" == "aarch64" ]]; then
    PCI_PREFIX="0001:1f"
else
    echo "Unsupported architecture: $ARCH"
    exit 1
fi

echo "Detected architecture: $ARCH, using PCI prefix: $PCI_PREFIX"

# Configure netdevsim interface pairs connected with a direct link (ptp1 <----> ptp1)
# ./configpair.sh <port1 id> <port2 id> <if1> <if2> <clk1> <clk2> \
#   <container1> <container2> <pci1> <pci2> [wpc]
#   wpc=1 enables DPLL + GNSS emulation on the port1 NIC (WPC NIC).

# TODO: netdevsim needs to implement real PCI address, right now we are reusing the address of existing devices

# Server 1 nic 1 (WPC — grandmaster with DPLL + GNSS) / nic 2 first ports to Server 2 nic 1
./configpair.sh 1 2 ens1f0 ens3f0 1 3 kind-netdevsim-worker kind-netdevsim-worker2 ${PCI_PREFIX}:01.0 ${PCI_PREFIX}:03.0 1
./configpair.sh 3 4 ens2f0 ens3f1 2 3 kind-netdevsim-worker kind-netdevsim-worker2 ${PCI_PREFIX}:02.0 ${PCI_PREFIX}:03.1

# Server 1 nic 1 (WPC) / nic 2 second ports to Server 3 nic 1 / nic 2
./configpair.sh 5 6 ens1f1 ens5f0 1 5 kind-netdevsim-worker kind-netdevsim-worker3 ${PCI_PREFIX}:01.1 ${PCI_PREFIX}:05.0 1
./configpair.sh 7 8 ens2f1 ens6f0 2 6 kind-netdevsim-worker kind-netdevsim-worker3 ${PCI_PREFIX}:02.1 ${PCI_PREFIX}:06.0

# Server 2 nic 2 (both ports share clock 4) to Server 3 nic 3 (both ports share clock 7)
./configpair.sh 9 10 ens4f0 ens7f0 4 7 kind-netdevsim-worker2 kind-netdevsim-worker3 ${PCI_PREFIX}:04.0 ${PCI_PREFIX}:07.0
./configpair.sh 11 12 ens4f1 ens7f1 4 7 kind-netdevsim-worker2 kind-netdevsim-worker3 ${PCI_PREFIX}:04.1 ${PCI_PREFIX}:07.1
