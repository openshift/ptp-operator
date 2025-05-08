JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
SUITE=../test/conformance
export KUBECONFIG=~/.kube/config
go install github.com/onsi/ginkgo/v2/ginkgo

export MAX_OFFSET_IN_NS=10000
export MIN_OFFSET_IN_NS=-10000

cat <<EOF > config.yaml
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
export KEEP_PTPCONFIG=false

systemctl stop chronyd

set -e
# OC
PTP_TEST_MODE=oc   ginkgo -v --focus=".*Slave can sync to master" --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# BC
PTP_TEST_MODE=bc KEEP_PTPCONFIG=true  ginkgo -v --focus=".*Slave can sync to master" --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual NIC BC
PTP_TEST_MODE=dualnicbc KEEP_PTPCONFIG=true  ginkgo -v --focus=".*Slave can sync to master" --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
# Dual port
PTP_TEST_MODE=dualfollower KEEP_PTPCONFIG=true  ginkgo -v --focus=".*Dual follower can" --focus=".*Slave can sync to master" --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v "$SUITE"/serial
