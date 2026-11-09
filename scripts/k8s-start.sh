#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Kind runs parallel `podman exec`; daemonless podman deadlocks on SQLite.
# Prefer a single API server when the helper is present (CI installs it).
if [[ -f "${SCRIPT_DIR}/start-podman-api-service.sh" ]]; then
  bash "${SCRIPT_DIR}/start-podman-api-service.sh" || true
fi
export PATH="/usr/local/ptp-podman-wrap:/usr/local/bin:${PATH}"

# Delete cluster
kind delete cluster --name kind-netdevsim

# Delete and re-create netdevsim devices
./reset-devices.sh

# Substitute registry IP into a temp copy so the tracked template stays clean.
KIND_CONFIG="$(mktemp "${PTP_RUN_DIR:-/tmp}/ptp-kind-config.XXXXXX.yaml")"
trap 'rm -f "${KIND_CONFIG}"' EXIT
sed "s/IP/$VM_IP/g" "${SCRIPT_DIR}/kind-config.yaml" >"${KIND_CONFIG}"

# Create new cluster
kind create cluster --name kind-netdevsim --config="${KIND_CONFIG}"

# Wait a bit until the cluster API becomes reacheable
./retry.sh 30 3 kubectl get nodes

# Wait for all pods to be ready
kubectl wait --for=condition=Ready pods -A --all --timeout=300s

# Apply some prerequisite configuration, such as node labels, etc
./prepare-kind.sh

# Create PriorityClass required by 'oc debug' (OpenShift-specific, missing on Kind)
cat <<EOF | kubectl apply -f -
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: openshift-user-critical
value: 1000000
description: "Required by oc debug"
EOF

# Deploy cert-manager (required by ptp) and wait for all pods to be ready
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml
kubectl wait --for=condition=Ready pods -A --all --timeout=300s

# Deploy prometheus (required by ptp) and wait for all pods to be ready
./deploy-prometheus.sh "$VM_IP"
kubectl wait --for=condition=Ready pods -A --all --timeout=300s 

# Create openshift-ptp namespace
kubectl create namespace openshift-ptp

# Configure netdevsim interfaces with direct server-to-server links
./configServers.sh

# Per-node stand-in mock PHCs for virtual CLOCK_REALTIME (phc2sys shim).
if [[ "${DKMS_MODE:-}" == "true" && "${PTP_VRT_ENABLE:-true}" == "true" ]]; then
    ./create-vrt-clocks.sh
fi