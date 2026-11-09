#!/bin/bash
set -x
set -euo pipefail

# Create a pair of netdevsim devices and link them together, one interface
# placed in each container's network namespace.
#
# Usage: ./configpair.sh <port1 netdevsim ID (unique)> <port2 netdevsim ID (unique)> \
#   <name of the interface on side 1 (ptp1)> <name of the interface on side 2 (ptp1)> \
#   <port1 ptp clock ID A (/dev/ptpA)> <port2 ptp clock ID B (/dev/ptpB)> \
#   <port1 container name> <port2 container name> <port1 pci ID> <port2 pci ID> \
#   [wpc]
#   wpc=1 enables DPLL + GNSS emulation on the port1 (side 1) NIC (WPC NIC).

NSIM_DEV_1_ID=$1
NSIM_DEV_2_ID=$2
IF1_NAME=$3
IF2_NAME=$4
CLK1=$5
CLK2=$6
CONTAINER1=$7
CONTAINER2=$8
PCI1=$9
PCI2=${10}
WPC=${11:-0}

NSIM_DEV_1_SYS=/sys/bus/pci/devices/$PCI1
NSIM_DEV_2_SYS=/sys/bus/pci/devices/$PCI2

NSIM_DEV_SYS_NEW=/sys/bus/netdevsim/new_device
NSIM_DEV_SYS_DEL=/sys/bus/netdevsim/del_device
NSIM_DEV_SYS_LINK=/sys/bus/netdevsim/link_device
NSIM_DEV_SYS_UNLINK=/sys/bus/netdevsim/unlink_device

cleanup_ns() {
    if [[ -n "${NSIM_DEV_1_FD:-}" && -n "${NSIM_DEV_2_FD:-}" && -n "${NSIM_DEV_1_IFIDX:-}" ]]; then
        echo "$NSIM_DEV_1_FD:$NSIM_DEV_1_IFIDX" >"$NSIM_DEV_SYS_UNLINK"
        echo $NSIM_DEV_2_ID >$NSIM_DEV_SYS_DEL || true
        echo $NSIM_DEV_1_ID >$NSIM_DEV_SYS_DEL || true
    fi
}

setup_ns() {

    NSIM_DEV_1_NAME=$(find $NSIM_DEV_1_SYS/net -maxdepth 1 -type d ! \
        -path $NSIM_DEV_1_SYS/net -exec basename {} \;)
    NSIM_DEV_2_NAME=$(find $NSIM_DEV_2_SYS/net -maxdepth 1 -type d ! \
        -path $NSIM_DEV_2_SYS/net -exec basename {} \;)

    PODMAN_NODE1_NS=$(get_container_netns_id $CONTAINER1)
    PODMAN_NODE2_NS=$(get_container_netns_id $CONTAINER2)

    ip link set dev $NSIM_DEV_1_NAME name $IF1_NAME
    ip link set $IF1_NAME netns $PODMAN_NODE1_NS
    ip netns exec $PODMAN_NODE1_NS ip link set $IF1_NAME netns $PODMAN_NODE1_NS
    ip netns exec $PODMAN_NODE1_NS ip link set $IF1_NAME address 00:11:22:33:"$NSIM_DEV_1_ID":"$NSIM_DEV_2_ID"
    ip netns exec $PODMAN_NODE1_NS ip link set dev $IF1_NAME up

    ip link set dev $NSIM_DEV_2_NAME name $IF2_NAME
    ip link set $IF2_NAME netns $PODMAN_NODE2_NS
    ip netns exec $PODMAN_NODE2_NS ip link set $IF2_NAME netns $PODMAN_NODE2_NS
    ip netns exec $PODMAN_NODE2_NS ip link set $IF2_NAME address 00:11:22:33:"$NSIM_DEV_2_ID":"$NSIM_DEV_1_ID"
    ip netns exec $PODMAN_NODE2_NS ip link set dev $IF2_NAME up
}

get_container_netns_id() {
    if [ -z "$1" ]; then
        echo "Usage: get_container_netns_id <container_name_or_id>"
        return 1
    fi

    local NETNS_ID=""

    # Try podman first (SandboxKey), then docker
    for runtime in podman docker; do
        command -v "$runtime" >/dev/null 2>&1 || continue

        local NETNS_PATH
        NETNS_PATH=$($runtime inspect --format '{{ .NetworkSettings.SandboxKey }}' "$1" 2>/dev/null) || continue
        NETNS_ID=$(basename "$NETNS_PATH")

        if [ -z "$NETNS_ID" ]; then
            local PID
            PID=$($runtime inspect --format '{{ .State.Pid }}' "$1" 2>/dev/null) || continue
            if [ -n "$PID" ] && [ "$PID" != "0" ]; then
                mkdir -p /var/run/netns
                NETNS_ID="ctr-$1"
                ln -sf "/proc/$PID/ns/net" "/var/run/netns/$NETNS_ID"
            fi
        fi

        [ -n "$NETNS_ID" ] && break
    done

    if [ -z "$NETNS_ID" ]; then
        echo "Error: Could not retrieve network namespace for container '$1' (tried podman, docker)." >&2
        return 1
    fi

    echo $NETNS_ID
}

# main
set -x

cleanup_ns

# Format: id pci clk [ports queues wpc] — wpc=1 enables DPLL + GNSS on that device.
echo "$NSIM_DEV_1_ID $PCI1 $CLK1 1 1 $WPC" >$NSIM_DEV_SYS_NEW
echo "$NSIM_DEV_2_ID $PCI2 $CLK2 1 1 0" >$NSIM_DEV_SYS_NEW
udevadm settle
setup_ns

NSIM_DEV_1_FD=$((256 + RANDOM % 256))
exec {NSIM_DEV_1_FD}</var/run/netns/$PODMAN_NODE1_NS
NSIM_DEV_1_IFIDX=$(ip netns exec $PODMAN_NODE1_NS cat /sys/class/net/$IF1_NAME/ifindex)

NSIM_DEV_2_FD=$((256 + RANDOM % 256))
exec {NSIM_DEV_2_FD}</var/run/netns/$PODMAN_NODE2_NS
NSIM_DEV_2_IFIDX=$(ip netns exec $PODMAN_NODE2_NS cat /sys/class/net/$IF2_NAME/ifindex)

echo "$NSIM_DEV_1_FD:$NSIM_DEV_1_IFIDX $NSIM_DEV_2_FD:$NSIM_DEV_2_IFIDX" >$NSIM_DEV_SYS_LINK
if [ $? -ne 0 ]; then
    echo "linking netdevsim1 with netdevsim2 should succeed"
    cleanup_ns
    exit 1
fi
