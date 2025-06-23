#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

# Delete cluster
kind delete cluster --name kind-netdevsim

# Delete and re-create netdevsim and openvswitch devices
./reset-devices.sh

# configure registry IP
sed -i "s/IP/$VM_IP/g" kind-config.yaml

# Create new cluster
kind create cluster --name kind-netdevsim --config=kind-config.yaml

# Wait a bit until the cluster API becomes reacheable
./retry.sh 30 3 kubectl get nodes

# Wait for all pods to be ready
kubectl wait --for=condition=Ready pods -A --all --timeout=300s

# Apply some prerequisite configuration, such as node labels, etc
./prepare-kind.sh

# Deploy cert-manager (required by ptp) and wait for all pods to be ready
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml
kubectl wait --for=condition=Ready pods -A --all --timeout=300s

# Deploy prometheus (required by ptp) and wait for all pods to be ready
./deploy-prometheus.sh
kubectl wait --for=condition=Ready pods -A --all --timeout=300s 

# Create openshift-ptp namespace
kubectl create namespace openshift-ptp

# Configure openvswitch and netdevsim interfaces 
./configSwitch2.sh "$VM_IP"