#!/bin/bash
set -x
set -euo pipefail

export DKMS_MODE="${DKMS_MODE:-false}"
TEST_MODES="oc,bc,dualnicbc,dualnicbcha,dualfollower"
RUN_PHASE="all"
REGISTRY_IP=""
TARBALL=""

while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --dkms)    export DKMS_MODE=true; shift ;;
        --mode)    TEST_MODES="$2"; shift 2 ;;
        --images)  RUN_PHASE="images"; shift ;;
        --deploy)  RUN_PHASE="deploy"; REGISTRY_IP="$2"; shift 2 ;;
        --load)    RUN_PHASE="load"; TARBALL="$2"; shift 2 ;;
        *) echo "Unknown flag: $1"; exit 1 ;;
    esac
done

VM_IP=$1

cd "$(dirname "${BASH_SOURCE[0]}")"
echo "Now in: $(pwd)"

export GOMAXPROCS=$(nproc)

source ./install-tools.sh

export BASHRCSOURCED=1
PS1="${PS1:-}" source ~/.bashrc

go mod tidy
go mod vendor

# ── Images phase (--images) ──────────────────────────────────────────
if [[ "$RUN_PHASE" == "images" ]]; then

    export IMG_PREFIX="$VM_IP/test"

    cd ../ptp-tools
    make -j8 podman-build-and-saveall
    cd -

    tar cf /tmp/ptp-images.tar -C /tmp/ptp-images .
    echo "Images saved to /tmp/ptp-images.tar ($(du -h /tmp/ptp-images.tar | cut -f1))"

fi

# ── Images + deploy (default) ───────────────────────────────────────
if [[ "$RUN_PHASE" == "all" ]]; then

    kind delete cluster --name kind-netdevsim || true
    podman rm -f switch1 || true

    export IMG_PREFIX="$VM_IP/test"

    cd ..
    make kustomize
    cd -

    ./create-local-registry.sh "$VM_IP"

    cd ../ptp-tools
    make podman-build-and-pushall
    cd -

fi

# ── Load phase (--load) ─────────────────────────────────────────────
if [[ "$RUN_PHASE" == "load" ]]; then

    export IMG_PREFIX="$VM_IP/test"

    mkdir -p /tmp/ptp-images-load
    tar xf "$TARBALL" -C /tmp/ptp-images-load
    TAGS=(lptpd cep ptpop krp openvswitch prometheus ptpmg debug)
    for t in "${TAGS[@]}"; do
        podman load -i "/tmp/ptp-images-load/$t.tar"
    done

    OLD_PREFIX=$(podman images --format '{{.Repository}}:{{.Tag}}' | grep ":${TAGS[0]}$" | head -1 | sed "s/:${TAGS[0]}$//")
    if [[ "$OLD_PREFIX" != "$IMG_PREFIX" ]]; then
        for t in "${TAGS[@]}"; do
            podman tag "$OLD_PREFIX:$t" "$IMG_PREFIX:$t"
        done
    fi

    cd ..
    make kustomize
    cd -

    ./create-local-registry.sh "$VM_IP"

    for t in "${TAGS[@]}"; do
        podman push "$IMG_PREFIX:$t" "docker://$IMG_PREFIX:$t" &
    done
    wait

fi

# ── Deploy phase (--deploy or default) ──────────────────────────────
if [[ "$RUN_PHASE" == "all" || "$RUN_PHASE" == "deploy" ]]; then

    if [[ "$RUN_PHASE" == "deploy" ]]; then
        export IMG_PREFIX="${REGISTRY_IP}/test"
    else
        export IMG_PREFIX="${IMG_PREFIX:-$VM_IP/test}"
    fi

    kind delete cluster --name kind-netdevsim || true
    podman rm -f switch1 || true

    ./k8s-start.sh "$VM_IP"

    cd ../ptp-tools
    make deploy-all || true
    sleep 5
    cd -

    kubectl apply -f certs.yaml
    ./retry.sh 60 5 ./fix-certs.sh
    sleep 5

    kubectl delete pods -l name=ptp-operator -n openshift-ptp
    kubectl rollout status deployment ptp-operator -n openshift-ptp

    ./retry.sh 60 5 kubectl patch ptpoperatorconfig default -nopenshift-ptp --type=merge --patch '{"spec": {"ptpEventConfig": {"enableEventPublisher": true, "transportHost": "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043", "storageType": "local-sc"}, "daemonNodeSelector": {"node-role.kubernetes.io/worker": ""}}}'

    ./retry.sh 30 3 kubectl rollout status ds linuxptp-daemon -n openshift-ptp

    ./fix-ptp-prometheus-monitoring.sh

    kubectl get pods -n openshift-ptp -o wide

    ./run-tests.sh --kind serial --mode "$TEST_MODES" \
      --linuxptp-daemon-image "$IMG_PREFIX:lptpd" \
      --must-gather-image "$IMG_PREFIX:ptpmg" \
      --debug-image "$IMG_PREFIX:debug"

fi
