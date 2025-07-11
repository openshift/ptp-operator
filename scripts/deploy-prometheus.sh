#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

podman tag "$VM_IP"/test:prometheus "$VM_IP"/test:3.4.2
podman push  "$VM_IP"/test:3.4.2

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add stable https://charts.helm.sh/stable
helm repo update
kubectl create namespace openshift-monitoring

helm install kind-prometheus prometheus-community/kube-prometheus-stack --namespace openshift-monitoring \
--set prometheus.service.nodePort=30000 \
--set prometheus.service.type=NodePort \
--set grafana.service.nodePort=31000 \
--set grafana.service.type=NodePort \
--set alertmanager.service.nodePort=32000 \
--set alertmanager.service.type=NodePort \
--set prometheus-node-exporter.service.nodePort=32001 \
--set prometheus-node-exporter.service.type=NodePort \
--set prometheus.prometheusSpec.image.registry="$VM_IP" \
--set prometheus.prometheusSpec.image.repository=test \
--set prometheus.prometheusSpec.image.tag=3.4.2 \
--set prometheus.prometheusSpec.image.pullPolicy=Always

