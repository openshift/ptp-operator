#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
SUITE=../test/conformance
export KUBECONFIG=~/.kube/config
go install github.com/onsi/ginkgo/v2/ginkgo

export MAX_OFFSET_IN_NS=10000
export MIN_OFFSET_IN_NS=-10000

cat <<EOF >config.yaml
global:
  maxoffset: $MAX_OFFSET_IN_NS
  minoffset: $MIN_OFFSET_IN_NS
  holdover_timeout: 5
  DisableAllSlaveRTUpdate: true
EOF
export USE_CONTAINER_CMDS=
export PTP_TEST_CONFIG_FILE="$(pwd)/config.yaml"
export PTP_LOG_LEVEL=info
export GOFLAGS=-mod=vendor
export KEEP_PTPCONFIG=true

export SKIP_INTERFACES="eth0"
export IMAGE_REGISTRY="$VM_IP/"
export CNF_TESTS_IMAGE=test:lptpd

# Function to disable switch1 authentication
disable_switch_auth() {
    echo "Disabling switch1 authentication..."
    podman cp ptpswitchconfig.cfg switch1:/etc/ptp4l.conf
    podman exec switch1 systemctl restart ptp4l
    echo "✓ Switch1 authentication disabled"
}

# Function to enable switch1 authentication
enable_switch_auth() {
    echo "Configuring switch1 with PTP authentication..."
    
    # 1. Copy auth-enabled ptp4l.conf to switch1
    podman cp test-config/ptpswitchconfig_auth.cfg switch1:/etc/ptp4l.conf
    
    # 2. Create directory and copy security file
    podman exec switch1 mkdir -p /etc/ptp-secret-mount/ptp-security-conf
    podman cp test-config/ptp-security.conf switch1:/etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf
    
    # 3. Restart ptp4l with authentication enabled
    podman exec switch1 systemctl restart ptp4l || {
    echo "WARNING: systemctl restart failed, trying pkill..."
    podman exec switch1 pkill ptp4l 2>/dev/null || true
    sleep 2
}
    
    echo "✓ Switch1 configured with authentication"
}

disable_switch_auth

systemctl stop chronyd

set -e
# OC
PTP_TEST_MODE=oc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# BC
PTP_TEST_MODE=bc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual NIC BC
PTP_TEST_MODE=dualnicbc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual NIC BC HA
PTP_TEST_MODE=dualnicbcha ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual port
PTP_TEST_MODE=dualfollower ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial

# Configure switch1 for authentication testing
# kubectl apply -f test-config/ptp-security.yaml
# enable_switch_auth

# Run tests with authentication enabled
# tests with auth will be enabled once the ci-github tests can last more than 1 hour 
# PTP_AUTH_ENABLED=true PTP_TEST_MODE=oc ginkgo --skip=".*The interfaces supporting ptp can be discovered correctly.*" --skip="Negative - run pmc in a new unprivileged pod on the slave node.*" -v --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
