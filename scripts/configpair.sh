#!/bin/bash
set -x
set -euo pipefail

NSIM_DEV_1_ID=$1
NSIM_DEV_2_ID=$2
NSIM_DEV_COMMON_NAME=$3
CLK1=$4
CLK2=$5
CONTAINER1=$6
CONTAINER2=$7
PCI1=$8
PCI2=$9

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

    PODMAN_NODE1_NS=$(get_podman_netns_id $CONTAINER1)
    PODMAN_NODE2_NS=$(get_podman_netns_id $CONTAINER2)

    ip link set dev $NSIM_DEV_1_NAME name $NSIM_DEV_COMMON_NAME
    ip link set $NSIM_DEV_COMMON_NAME netns $PODMAN_NODE1_NS
    ip netns exec $PODMAN_NODE1_NS ip link set $NSIM_DEV_COMMON_NAME netns $PODMAN_NODE1_NS
    ip netns exec $PODMAN_NODE1_NS ip link set $NSIM_DEV_COMMON_NAME address 00:11:22:33:"$NSIM_DEV_1_ID":"$NSIM_DEV_2_ID"

    ip link set dev $NSIM_DEV_2_NAME name $NSIM_DEV_COMMON_NAME
    ip link set $NSIM_DEV_COMMON_NAME netns $PODMAN_NODE2_NS
    ip netns exec $PODMAN_NODE2_NS ip link set $NSIM_DEV_COMMON_NAME netns $PODMAN_NODE2_NS
    ip netns exec $PODMAN_NODE2_NS ip link set $NSIM_DEV_COMMON_NAME address 00:11:22:33:"$NSIM_DEV_2_ID":"$NSIM_DEV_1_ID"

    ip netns exec $PODMAN_NODE1_NS ip link set dev $NSIM_DEV_COMMON_NAME up
    ip netns exec $PODMAN_NODE2_NS ip link set dev $NSIM_DEV_COMMON_NAME up
}

get_podman_netns_id() {
    if [ -z "$1" ]; then
        echo "Usage: get_podman_netns_id <container_name_or_id>"
        return 1
    fi

    # Get the full SandboxKey path
    local NETNS_PATH
    NETNS_PATH=$(podman inspect --format '{{ .NetworkSettings.SandboxKey }}' "$1" 2>/dev/null)

    # Extract only the namespace ID
    local NETNS_ID
    NETNS_ID=$(basename "$NETNS_PATH")

    # Check if we got a valid namespace ID
    if [ -z "$NETNS_ID" ]; then
        echo "Error: Could not retrieve network namespace ID for container '$1'."
        return 1
    fi

    echo "$NETNS_ID"
}

# main
set -x

cleanup_ns

echo "$NSIM_DEV_1_ID $PCI1 $CLK1" >$NSIM_DEV_SYS_NEW
echo "$NSIM_DEV_2_ID $PCI2 $CLK2" >$NSIM_DEV_SYS_NEW
udevadm settle
setup_ns

NSIM_DEV_1_FD=$((256 + RANDOM % 256))
exec {NSIM_DEV_1_FD}</var/run/netns/$PODMAN_NODE1_NS
NSIM_DEV_1_IFIDX=$(ip netns exec $PODMAN_NODE1_NS cat /sys/class/net/$NSIM_DEV_COMMON_NAME/ifindex)

NSIM_DEV_2_FD=$((256 + RANDOM % 256))
exec {NSIM_DEV_2_FD}</var/run/netns/$PODMAN_NODE2_NS
NSIM_DEV_2_IFIDX=$(ip netns exec $PODMAN_NODE2_NS cat /sys/class/net/$NSIM_DEV_COMMON_NAME/ifindex)

echo "$NSIM_DEV_1_FD:$NSIM_DEV_1_IFIDX $NSIM_DEV_2_FD:$NSIM_DEV_2_IFIDX" >$NSIM_DEV_SYS_LINK
if [ $? -ne 0 ]; then
    echo "linking netdevsim1 with netdevsim2 should succeed"
    cleanup_ns
    exit 1
fi
