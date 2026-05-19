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

# ── Images phase (--images or default) ──────────────────────────────
if [[ "$RUN_PHASE" == "all" || "$RUN_PHASE" == "images" ]]; then

    kind delete cluster --name kind-netdevsim || true
    podman rm -f switch1 || true

    export IMG_PREFIX="$VM_IP/test"

    cd ../ptp-tools
    make -j 8 podman-cleanall
    make -j 8 podman-buildall
    cd -

    cd ..
    make kustomize
    cd -

    ./create-local-registry.sh "$VM_IP"

    cd ../ptp-tools
    make podman-pushall
    cd -

    if [[ "$RUN_PHASE" == "images" ]]; then
        TAGS=(lptpd cep ptpop krp openvswitch prometheus ptpmg debug)
        SAVE_ARGS=()
        for t in "${TAGS[@]}"; do SAVE_ARGS+=("$IMG_PREFIX:$t"); done
        podman save -m -o /tmp/ptp-images.tar "${SAVE_ARGS[@]}"
        echo "Images saved to /tmp/ptp-images.tar ($(du -h /tmp/ptp-images.tar | cut -f1))"
    fi

fi

# ── Load phase (--load) ─────────────────────────────────────────────
if [[ "$RUN_PHASE" == "load" ]]; then

    export IMG_PREFIX="$VM_IP/test"

    podman load -i "$TARBALL"

    TAGS=(lptpd cep ptpop krp openvswitch prometheus ptpmg debug)
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

    SYMLINK_PID=""
    if [[ "${DKMS_MODE}" == "true" ]]; then
        bash -c '
        while true; do
          for pod in $(kubectl get pods -n openshift-ptp -l app=linuxptp-daemon \
                       --field-selector=status.phase=Running -o name 2>/dev/null); do
            kubectl exec -n openshift-ptp ${pod#pod/} -c linuxptp-daemon-container -- \
              bash -c "for i in 0 1 2 3 4 5 6 7 8 9; do ln -sf nsim_ptp\$i /dev/ptp\$i 2>/dev/null; done" \
              2>/dev/null || true
          done
          sleep 5
        done
        ' &
        SYMLINK_PID=$!
        echo "Symlink maintainer PID: $SYMLINK_PID"
        cleanup_symlink() { [[ -n "$SYMLINK_PID" ]] && kill "$SYMLINK_PID" 2>/dev/null || true; }
        trap cleanup_symlink EXIT
    fi

    ./run-tests.sh --kind serial --mode "$TEST_MODES" \
      --linuxptp-daemon-image "$IMG_PREFIX:lptpd" \
      --must-gather-image "$IMG_PREFIX:ptpmg" \
      --debug-image "$IMG_PREFIX:debug"

fi
