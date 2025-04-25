#!/bin/bash
set -x

VM_IP=$1

# Navigate to the directory where this script is located
cd "$(dirname "${BASH_SOURCE[0]}")"

echo "Now in: $(pwd)"

export GOMAXPROCS=$(nproc)

# disable firewall
systemctl disable firewalld --now

./install-tools.sh

# load go
source /root/.gvm/scripts/gvm
gvm use go1.23.0

# Build images
cd ../ptp-tools
# configure images for local registry
sed -i "s|IMG_PREFIX ?= quay.io/deliedit/test|IMG_PREFIX ?= $VM_IP/test|" ../ptp-tools/Makefile

make -j 5 podman-buildall
cd -

# build kustomize
cd ..
make kustomize
cd -
./create-local-registry.sh "$VM_IP"

# push images
cd ../ptp-tools
make -j 5 podman-pushall
cd -

# deploy kind cluster
./k8s-start.sh "$VM_IP"

# deploy ptp-operator
cd ../ptp-tools
make deploy-all
sleep 5
cd -
# build certificates
cd ..
rm -rf bin/go
go mod tidy
go mod vendor
make certs
cd -
# fix certificates
./retry.sh 60 5 ./fix-certs.sh

# delete ptp-operator
kubectl delete pods -l name=ptp-operator -n openshift-ptp

# wait for operator to come up
kubectl rollout status deployment ptp-operator -n openshift-ptp

# Patch ptpoperatorconfig to start events (in case it is not configured yet )
./retry.sh 60 5 kubectl patch ptpoperatorconfig default -nopenshift-ptp --type=merge --patch '{"spec": {"ptpEventConfig": {"enableEventPublisher": true, "transportHost": "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043", "storageType": "local-sc"}, "daemonNodeSelector": {"node-role.kubernetes.io/worker": ""}}}'

./retry.sh 30 3 kubectl rollout status ds linuxptp-daemon -n openshift-ptp
kubectl get pods -n openshift-ptp -o wide

# run tests
./run-ci-github.sh 
