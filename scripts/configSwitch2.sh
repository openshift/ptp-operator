#!/bin/bash
set -x
set -euo pipefail
VM_IP=$1

# Create openvswitch container
podman run -d --privileged --volume /dev:/dev --replace --pull always --name switch1 "$VM_IP"/test:openvswitch

# Install extra tools (to be integrated in openvswitch container image)
podman exec switch1 yum install iputils iproute ptp4l ethtool ps -y

# Configure netdevsim interface pairs connected with veth (ptp1 <----> ptp1)
# ./configpair.sh < port1 netdevsim ID (unique)> <port 2 netdevsim ID (unique)> <name of the interface on both side of the link (ptp1)>
# <port1 ptp clock ID A (/dev/ptpA)> < port2 ptp clock ID B (/dev/ptpB)> <port1 container name > <port2 container name> <port1 pci ID> <port2 pci ID>

# TODO: netdevsim needs to implement real PCI address, right now we are reusing the address of existing devices
# Server 1 nic 1
./configpair.sh 1 2 ens1f0 1 0 kind-netdevsim-worker switch1 0000:00:01.0 0000:00:01.0
./configpair.sh 3 4 ens1f1 1 0 kind-netdevsim-worker switch1 0000:00:01.0 0000:00:02.0

# Server 1 nic 2
./configpair.sh 5 6 ens2f0 2 0 kind-netdevsim-worker switch1 0000:00:03.0 0000:00:03.0
./configpair.sh 7 8 ens2f1 2 0 kind-netdevsim-worker switch1 0000:00:03.0 0000:00:04.0

# Server 2 nic 1
./configpair.sh 9 10 ens3f0 3 0 kind-netdevsim-worker2 switch1 0000:00:01.0 0000:00:05.0
./configpair.sh 11 12 ens3f1 3 0 kind-netdevsim-worker2 switch1 0000:00:01.0 0000:00:06.0

# Server 3 nic 1
./configpair.sh 13 14 ens4f0 4 0 kind-netdevsim-worker3 switch1 0000:00:01.0 0000:00:07.0

# Server 3 nic 2
./configpair.sh 15 16 ens5f0 5 0 kind-netdevsim-worker3 switch1 0000:00:03.0 0000:00:08.0

# Server 3 nic 3
./configpair.sh 17 18 ens6f0 6 0 kind-netdevsim-worker3 switch1 0000:00:04.0 0000:00:09.0

# start openvswitch service
$(podman exec switch1 systemctl enable --now openvswitch)|| {
    status=$?
    echo "❌ command failed with code $status"
    podman exec switch1 systemctl start openvswitch || true
    podman exec switch1 systemctl status openvswitch
    podman exec switch1 journalctl -u openvswitch
    exit $status
}

# Configure openvswitch bridge with netdevsim ports
podman exec switch1 ovs-vsctl add-br br0
# vlan 1501
podman exec switch1 ovs-vsctl add-port br0 ens1f0 tag=1501
podman exec switch1 ovs-vsctl add-port br0 ens4f0 tag=1501
# vlan 1502
podman exec switch1 ovs-vsctl add-port br0 ens1f1 tag=1500
podman exec switch1 ovs-vsctl add-port br0 ens2f0 tag=1500
podman exec switch1 ovs-vsctl add-port br0 ens3f0 tag=1500
podman exec switch1 ovs-vsctl add-port br0 ens3f1 tag=1500
# vlan 1503
podman exec switch1 ovs-vsctl add-port br0 ens2f1 tag=1502
podman exec switch1 ovs-vsctl add-port br0 ens5f0 tag=1502
podman exec switch1 ovs-vsctl add-port br0 ens6f0 tag=1502

# turn everything up at the switch
podman exec switch1 ip link set dev br0 up
podman exec switch1 ip link set dev ens1f0 up
podman exec switch1 ip link set dev ens1f1 up
podman exec switch1 ip link set dev ens2f0 up
podman exec switch1 ip link set dev ens2f1 up
podman exec switch1 ip link set dev ens3f0 up
podman exec switch1 ip link set dev ens3f1 up
podman exec switch1 ip link set dev ens4f0 up
podman exec switch1 ip link set dev ens5f0 up
podman exec switch1 ip link set dev ens6f0 up

# Disable ptp4l frame forwarding
podman exec switch1 ovs-ofctl add-flow br0 "dl_type=0x88f7, actions=drop"

# Configure and start ptp4l boundary clock on the bridge ports
podman cp ptpswitchconfig.cfg switch1:/etc/ptp4l.conf

$(podman exec switch1 systemctl enable --now ptp4l) || {
    status=$?
    echo "❌ command failed with code $status"
    podman exec switch1 systemctl start ptp4l || true
    podman exec switch1 systemctl status ptp4l
    podman exec switch1 journalctl -u ptp4l
    exit $status
}
