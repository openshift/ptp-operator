#!/bin/bash
#
# Usage: ./run-tests.sh --kind <serial|parallel|both> --mode <modes> [OPTIONS]
#
# Required flags:
#   --kind <serial|parallel|both>   Test run kind
#   --mode <modes>                  Comma-separated test modes (e.g. oc,bc,dualnicbc)
#
# Optional flags:
#   --loglevel <level>              PTP log level (default: info)
#   --focus <regex>                 Pass through to ginkgo --focus (run matching specs only)
#   --linuxptp-daemon-image <url>   Full image URL for the linuxptp-daemon test pod.
#                                   When omitted, pmc pod tests are skipped.
#   --must-gather-image <url>       Full image URL for the ptp must-gather image.
#                                   When provided, must-gather runs for oc mode and on failure.
#
# Environment:
# Environment:
#   GNSS_SIM_DEPLOY=auto|on|off     Deploy gnss-sim for tgm/tgmoc/tgmbc (default: auto =
#                                   only when a Kind netdevsim cluster is detected).
#   PTP_AUTH_ENABLED=true          Run tests with PTP authentication enabled
#                                  (applies test-config/ptp-security.yaml).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GINKGO_HEADLINE_REWRITE="${SCRIPT_DIR}/ginkgo-headline-rewrite.sh"

usage() {
  echo "Usage: $0 --kind <serial|parallel|both> --mode <modes> [--focus <regex>] [--loglevel <level>] [--linuxptp-daemon-image <url>] [--must-gather-image <url>]"
  exit 1
}

RUN_KIND=""
TEST_MODES_RAW=""
PTP_LOG_LEVEL="info"
LINUXPTP_DAEMON_IMAGE=""
MUST_GATHER_IMAGE=""
GINKGO_FOCUS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --kind)
      RUN_KIND="$2"; shift 2 ;;
    --mode)
      TEST_MODES_RAW="$2"; shift 2 ;;
    --loglevel)
      PTP_LOG_LEVEL="$2"; shift 2 ;;
    --focus)
      [[ $# -ge 2 ]] || usage
      GINKGO_FOCUS="$2"; shift 2 ;;
    --linuxptp-daemon-image)
      LINUXPTP_DAEMON_IMAGE="$2"; shift 2 ;;
    --must-gather-image)
      MUST_GATHER_IMAGE="$2"; shift 2 ;;
    --debug-image)
      DEBUG_IMAGE="$2"; shift 2 ;;
    *)
      echo "Unknown flag: $1"
      usage ;;
  esac
done

if [[ -z "${RUN_KIND}" || -z "${TEST_MODES_RAW}" ]]; then
  echo "Error: --kind and --mode are required."
  usage
fi

JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
SUITE=../test/conformance
export KUBECONFIG=${KUBECONFIG:-~/.kube/config}
go install github.com/onsi/ginkgo/v2/ginkgo

mkdir -p "$JUNIT_OUTPUT_DIR"

export MAX_OFFSET_IN_NS="${MAX_OFFSET_IN_NS:-10000}"
export MIN_OFFSET_IN_NS="${MIN_OFFSET_IN_NS:--10000}"
export COLLECT_POD_LOGS="${COLLECT_POD_LOGS:-true}"
export LOG_ARTIFACTS_DIR="${LOG_ARTIFACTS_DIR:-${JUNIT_OUTPUT_DIR}/pod-logs}"

# With VRT, each Kind worker disciplines its own stand-in mock PHC instead of
# the shared host CLOCK_REALTIME, so slave phc2sys -a -r is safe.
# Enable RT update when:
#   - DISABLE_SLAVE_RT_UPDATE is set explicitly, or
#   - PTP_VRT_ENABLE=false → keep disabled, or
#   - DKMS_MODE=true (install/CI path), or
#   - VRT device files already exist from a prior cluster install
#     (re-running ./run-tests.sh without DKMS_MODE).
vrt_already_present() {
  local pod ns=openshift-ptp
  while read -r pod; do
    [[ -z "$pod" ]] && continue
    if kubectl exec -n "$ns" "$pod" -c linuxptp-daemon-container -- \
        test -s /var/run/vrt/device &>/dev/null; then
      return 0
    fi
  done < <(kubectl get pods -n "$ns" -l app=linuxptp-daemon \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)

  local node
  for node in kind-netdevsim-worker kind-netdevsim-worker2 kind-netdevsim-worker3; do
    if podman exec "$node" test -s /var/run/ptp/vrt/device &>/dev/null ||
      docker exec "$node" test -s /var/run/ptp/vrt/device &>/dev/null; then
      return 0
    fi
  done
  return 1
}

if [[ -n "${DISABLE_SLAVE_RT_UPDATE:-}" ]]; then
  :
elif [[ "${PTP_VRT_ENABLE:-true}" != "true" ]]; then
  DISABLE_SLAVE_RT_UPDATE=true
elif [[ "${DKMS_MODE:-}" == "true" ]] || vrt_already_present; then
  DISABLE_SLAVE_RT_UPDATE=false
else
  DISABLE_SLAVE_RT_UPDATE=true
fi
echo "DisableAllSlaveRTUpdate=${DISABLE_SLAVE_RT_UPDATE}"

CONFIG_YAML="$(mktemp "${PTP_RUN_DIR:-/tmp}/ptp-config.XXXXXX.yaml")"
cat <<EOF >"${CONFIG_YAML}"
global:
  maxoffset: $MAX_OFFSET_IN_NS
  minoffset: $MIN_OFFSET_IN_NS
  holdover_timeout: 5
  DisableAllSlaveRTUpdate: ${DISABLE_SLAVE_RT_UPDATE}
EOF
export USE_CONTAINER_CMDS=
export PTP_TEST_CONFIG_FILE="${CONFIG_YAML}"
export PTP_LOG_LEVEL
export GOFLAGS=-mod=vendor
export KEEP_PTPCONFIG="${KEEP_PTPCONFIG:-true}"

export SKIP_INTERFACES="${SKIP_INTERFACES:-eth0}"

if [[ -n "${LINUXPTP_DAEMON_IMAGE}" ]]; then
  export IMAGE_REGISTRY="${LINUXPTP_DAEMON_IMAGE%/*}/"
  export CNF_TESTS_IMAGE="${LINUXPTP_DAEMON_IMAGE##*/}"
fi

IFS=',' read -r -a TEST_MODES <<< "${TEST_MODES_RAW}"

SKIP_PATTERNS=(
  ".*The interfaces supporting ptp can be discovered correctly.*"
  "Negative - run pmc in a new unprivileged pod on the slave node.*"
)

if [[ -z "${LINUXPTP_DAEMON_IMAGE}" ]]; then
  SKIP_PATTERNS+=(
    "Run pmc in a new pod on the slave node.*"
  )
fi

case "${RUN_KIND}" in
  serial|parallel|both) ;;
  *)
    echo "Invalid RUN_KIND value: ${RUN_KIND}. Use serial, parallel, or both."
    exit 2
    ;;
esac

MUST_GATHER_RAN=false

run_must_gather() {
  if [[ -z "${MUST_GATHER_IMAGE}" ]]; then
    echo "No must-gather image provided, skipping must-gather collection."
    return 0
  fi
  if [[ "${MUST_GATHER_RAN}" == "true" ]]; then
    return 0
  fi
  MUST_GATHER_RAN=true

  echo "Running must-gather with image: ${MUST_GATHER_IMAGE}"
  oc adm must-gather \
    --image="${MUST_GATHER_IMAGE}" \
    --node-name=kind-netdevsim-worker \
    --dest-dir="${JUNIT_OUTPUT_DIR}/must-gather" \
    --volume-percentage=95 \
    -- /usr/bin/gather --debug-image="${DEBUG_IMAGE}" || \
    echo "WARNING: must-gather collection failed"
}

on_exit() {
  local exit_code=$?
  rm -f "${CONFIG_YAML:-}"
  if [[ ${exit_code} -ne 0 ]]; then
    echo "Script failed with exit code ${exit_code}, collecting must-gather..."
    run_must_gather
  fi
}
trap on_exit EXIT

export PTP_AUTH_ENABLED="${PTP_AUTH_ENABLED:-false}"
if [[ "${PTP_AUTH_ENABLED}" == "true" ]]; then
  echo "PTP authentication enabled, applying ptp-security-conf Secret"
  kubectl apply -f "${SCRIPT_DIR}/test-config/ptp-security.yaml"
fi

systemctl stop chronyd 2>/dev/null || systemctl stop chrony 2>/dev/null || true

set -e

run_ginkgo_suite() {
  local mode="$1"
  local suite_kind="$2"
  local junit_base="${JUNIT_OUTPUT_FILE%.xml}"
  local ginkgo_args=(
    --keep-going
    --flake-attempts="${FLAKE_ATTEMPTS:-2}"
    --output-dir="${JUNIT_OUTPUT_DIR}"
    --junit-report="${junit_base}_${mode}_${suite_kind}.xml"
    -v
  )

  if [[ -n "${GINKGO_FOCUS}" ]]; then
    ginkgo_args+=(--focus="${GINKGO_FOCUS}")
  fi

  for skip in "${SKIP_PATTERNS[@]}"; do
    ginkgo_args+=(--skip="${skip}")
  done

  local ginkgo_rc
  set +e
  if [[ "${PTP_GINKGO_HEADLINE_REWRITE:-1}" != "0" ]] && [[ -f "${GINKGO_HEADLINE_REWRITE}" ]]; then
    case "${suite_kind}" in
      parallel)
        PTP_TEST_MODE="${mode}" ginkgo -p "${ginkgo_args[@]}" "${SUITE}/parallel" 2>&1 | bash "${GINKGO_HEADLINE_REWRITE}"
        ;;
      *)
        PTP_TEST_MODE="${mode}" ginkgo "${ginkgo_args[@]}" "${SUITE}/serial" 2>&1 | bash "${GINKGO_HEADLINE_REWRITE}"
        ;;
    esac
    ginkgo_rc=${PIPESTATUS[0]}
  else
    case "${suite_kind}" in
      parallel)
        PTP_TEST_MODE="${mode}" ginkgo -p "${ginkgo_args[@]}" "${SUITE}/parallel"
        ;;
      *)
        PTP_TEST_MODE="${mode}" ginkgo "${ginkgo_args[@]}" "${SUITE}/serial"
        ;;
    esac
    ginkgo_rc=$?
  fi
  set -e
  return "${ginkgo_rc}"
}

# gnss-sim (and GNSS_SIM_* env) is T-GM only. Exporting GNSS_SIM_IFACE* for
# OC/BC/DualNIC* lets the WPC L2 overlay / solver pin DualNICBC onto worker1
# GNSS NICs, and CLOCK_REALTIME never locks on the BC. Deploy the pod once
# before the mode loop so we do not bounce it between suites.
is_telco_gm_mode() {
  case "${1,,}" in
    tgm|tgmoc|tgmbc) return 0 ;;
    *) return 1 ;;
  esac
}

modes_include_telco_gm() {
  local mode
  for mode in "${TEST_MODES[@]}"; do
    if is_telco_gm_mode "${mode}"; then
      return 0
    fi
  done
  return 1
}

# Deploy gnss-sim once for the Kind/netdevsim cluster (idempotent). Does not
# export GNSS_SIM_IFACE* — those stay T-GM-mode-only via export_gnss_sim_env.
#
# GNSS_SIM_DEPLOY controls whether the simulator is installed:
#   auto (default) — deploy only when a Kind netdevsim cluster is detected
#                    and --mode includes tgm/tgmoc/tgmbc
#   on|true|1|yes  — force deploy (even on baremetal) when T-GM modes are present
#   off|false|0|no — never deploy (baremetal / hardware GNSS)
deploy_gnss_sim() {
  export GNSS_SIM_API_PORT="${GNSS_SIM_API_PORT:-9200}"

  # Derive GNSS_SIM_IMAGE from the linuxptp-daemon image if not already set,
  # by replacing the tag. This handles direct invocation of run-tests.sh.
  if [ -z "${GNSS_SIM_IMAGE:-}" ] && [ -n "${LINUXPTP_DAEMON_IMAGE:-}" ]; then
    export GNSS_SIM_IMAGE="${LINUXPTP_DAEMON_IMAGE%:*}:gnss-sim"
  fi

  bash "${SCRIPT_DIR}/configGNSS.sh"
}

is_kind_netdevsim_cluster() {
  kubectl get nodes -o name 2>/dev/null | grep -q 'node/kind-netdevsim'
}

should_deploy_gnss_sim() {
  if ! modes_include_telco_gm; then
    return 1
  fi
  local mode="${GNSS_SIM_DEPLOY:-auto}"
  case "${mode,,}" in
    on|true|1|yes)
      return 0
      ;;
    off|false|0|no)
      return 1
      ;;
    auto|"")
      is_kind_netdevsim_cluster
      ;;
    *)
      echo "Error: invalid GNSS_SIM_DEPLOY=${mode} (use auto|on|off)" >&2
      exit 1
      ;;
  esac
}

# Export GNSS_SIM_* for the test framework for a T-GM mode run. Assumes the
# pod was already deployed by deploy_gnss_sim.
export_gnss_sim_env() {
  export GNSS_SIM_API_PORT="${GNSS_SIM_API_PORT:-9200}"
  export GNSS_SIM_API_HOST="$(cat /tmp/gnss-sim-api-host 2>/dev/null || echo localhost)"

  GNSS_KERNEL_DEV=""
  for g in /dev/gnss*; do
    [ -c "$g" ] && GNSS_KERNEL_DEV="$g" && break
  done
  if [ -n "$GNSS_KERNEL_DEV" ]; then
    export GNSS_SIM_NMEA_DEVICE="${GNSS_SIM_NMEA_DEVICE:-$(basename "$GNSS_KERNEL_DEV")}"
  else
    # Must match gnss-sim --pty-links and the daemon's /var/run/ptp mount.
    export GNSS_SIM_NMEA_DEVICE="${GNSS_SIM_NMEA_DEVICE:-/var/run/ptp/ttyGNSS_TS2PHC}"
  fi
  export GNSS_SIM_IFACE1="${GNSS_SIM_IFACE1:-ens1f0}"
  export GNSS_SIM_IFACE2="${GNSS_SIM_IFACE2:-ens1f1}"
}

clear_gnss_sim_env() {
  unset GNSS_SIM_API_HOST GNSS_SIM_NMEA_DEVICE GNSS_SIM_IFACE1 GNSS_SIM_IFACE2
}

GNSS_SIM_DEPLOYED=0
if should_deploy_gnss_sim; then
  echo "Deploying gnss-sim once before mode loop (GNSS_SIM_DEPLOY=${GNSS_SIM_DEPLOY:-auto})"
  deploy_gnss_sim
  GNSS_SIM_DEPLOYED=1
elif modes_include_telco_gm; then
  echo "Skipping gnss-sim deploy (GNSS_SIM_DEPLOY=${GNSS_SIM_DEPLOY:-auto}; Kind/netdevsim not detected — use GNSS_SIM_DEPLOY=on to force)"
else
  echo "Skipping gnss-sim deploy (no tgm/tgmoc/tgmbc in --mode)"
fi

overall_exit=0
for mode in "${TEST_MODES[@]}"; do
  if is_telco_gm_mode "${mode}" && [[ "${GNSS_SIM_DEPLOYED}" -eq 1 ]]; then
    echo "Exporting GNSS_SIM_* env for T-GM mode ${mode}"
    export_gnss_sim_env
  else
    echo "Clearing GNSS_SIM_* env for mode ${mode} (T-GM only: tgm, tgmoc, tgmbc)"
    clear_gnss_sim_env
  fi
  if [[ "${RUN_KIND}" == "serial" || "${RUN_KIND}" == "both" ]]; then
    run_ginkgo_suite "${mode}" "serial" || overall_exit=1
  fi
  if [[ "${RUN_KIND}" == "parallel" || "${RUN_KIND}" == "both" ]]; then
    run_ginkgo_suite "${mode}" "parallel" || overall_exit=1
  fi
done

# Run must-gather for oc mode
for mode in "${TEST_MODES[@]}"; do
  if [[ "${mode}" == "oc" ]]; then
    echo "OC mode detected, collecting must-gather..."
    run_must_gather
    break
  fi
done
exit ${overall_exit}
