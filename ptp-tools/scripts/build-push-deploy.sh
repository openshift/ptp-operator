#!/bin/bash
# Build, push, and deploy ptp-operator, linuxptp-daemon, and cloud-event-proxy
# from specific remote branches. Never builds local code.
#
# Usage:
#   ./scripts/build-push-deploy.sh [--ptpop <branch-spec>] [--lptpd <branch-spec>] [--cep <branch-spec>] [phases]
#
# At least one component flag is required. Only specified components are built/pushed.
#
# Branch spec formats:
#   Shorthand:  upstream/main  or  downstream/release-4.22
#   Commit ID:  downstream/a1b2c3d  (7-40 hex chars = checkout specific commit)
#   Custom:     edcdavid/cloud-event-proxy/fix-my-bug
#
# Phase flags (if none specified, all phases run):
#   --build    Build images
#   --push     Push images
#   --deploy   Deploy to cluster
#   --check    Verify running image commits match expected branch HEADs
#
# Shorthand repo mapping:
#   Component  upstream                                    downstream
#   ptpop      k8snetworkplumbingwg/ptp-operator           openshift/ptp-operator
#   lptpd      k8snetworkplumbingwg/linuxptp-daemon        openshift/linuxptp-daemon
#   cep        redhat-cne/cloud-event-proxy                redhat-cne/cloud-event-proxy
#
# IMG_PREFIX can be set via environment or --img-prefix flag.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOLS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

PTPOP_SPEC=""
LPTPD_SPEC=""
CEP_SPEC=""
LINUXPTP_RPM_PATH=""
DO_BUILD=false
DO_PUSH=false
DO_DEPLOY=false
DO_CHECK=false

usage() {
  echo "Usage: $0 [--ptpop <branch-spec>] [--lptpd <branch-spec>] [--cep <branch-spec>] [options]"
  echo ""
  echo "At least one component flag is required. Only specified components are built/pushed."
  echo ""
  echo "Branch spec formats:"
  echo "  upstream/<branch>                       Well-known upstream repo"
  echo "  downstream/<branch>                     Well-known downstream repo"
  echo "  downstream/<commit-id>                  Checkout specific commit (7-40 hex chars)"
  echo "  <github-org>/<repo>/<branch>            Custom fork/repo"
  echo ""
  echo "Phase flags (if none given, build+push+deploy run):"
  echo "  --build                                 Build images"
  echo "  --push                                  Push images"
  echo "  --deploy                                Deploy to cluster"
  echo "  --check                                 Verify running commits match branch HEADs"
  echo ""
  echo "Other options:"
  echo "  --img-prefix <prefix>                   Override IMG_PREFIX"
  echo "  --linuxptp-rpm <path>                   Path to a pre-built linuxptp RPM to install"
  echo "                                          in the lptpd image instead of the stock one."
  echo "                                          Requires --lptpd to be specified."
  echo ""
  echo "Examples:"
  echo "  $0 --ptpop downstream/release-4.20 --lptpd downstream/release-4.20"
  echo "  $0 --cep downstream/a1b2c3d4e5f6                   # build from specific commit"
  echo "  $0 --lptpd upstream/main --linuxptp-rpm /path/to/linuxptp-4.4-1.el9.4.rpm"
  echo "  $0 --cep edcdavid/cloud-event-proxy/fix-tbc --build --push --deploy"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ptpop) PTPOP_SPEC="$2"; shift 2 ;;
    --lptpd) LPTPD_SPEC="$2"; shift 2 ;;
    --cep)   CEP_SPEC="$2";   shift 2 ;;
    --img-prefix) export IMG_PREFIX="$2"; shift 2 ;;
    --linuxptp-rpm) LINUXPTP_RPM_PATH="$2"; shift 2 ;;
    --build)  DO_BUILD=true;  shift ;;
    --push)   DO_PUSH=true;   shift ;;
    --deploy) DO_DEPLOY=true; shift ;;
    --check)  DO_CHECK=true;  shift ;;
    -h|--help) usage ;;
    *) echo "Unknown flag: $1"; usage ;;
  esac
done

# If no phase flags given, run build+push+deploy (not check)
if [[ "$DO_BUILD" == false && "$DO_PUSH" == false && "$DO_DEPLOY" == false && "$DO_CHECK" == false ]]; then
  DO_BUILD=true
  DO_PUSH=true
  DO_DEPLOY=true
fi

# Validate --linuxptp-rpm
LINUXPTP_RPM_FILENAME=""
if [[ -n "$LINUXPTP_RPM_PATH" ]]; then
  if [[ -z "$LPTPD_SPEC" ]]; then
    echo "Error: --linuxptp-rpm requires --lptpd <branch-spec>"
    exit 1
  fi
  if [[ ! -f "$LINUXPTP_RPM_PATH" ]]; then
    echo "Error: RPM file not found: $LINUXPTP_RPM_PATH"
    exit 1
  fi
  LINUXPTP_RPM_FILENAME="$(basename "$LINUXPTP_RPM_PATH")"
fi

if [[ -z "$PTPOP_SPEC" && -z "$LPTPD_SPEC" && -z "$CEP_SPEC" ]]; then
  echo "Error: at least one of --ptpop, --lptpd, or --cep is required."
  usage
fi

TARGETS=()

# --- Repo mapping tables ---

declare -A UPSTREAM_REPOS=(
  [ptpop]="k8snetworkplumbingwg/ptp-operator"
  [lptpd]="k8snetworkplumbingwg/linuxptp-daemon"
  [cep]="redhat-cne/cloud-event-proxy"
)

declare -A DOWNSTREAM_REPOS=(
  [ptpop]="openshift/ptp-operator"
  [lptpd]="openshift/linuxptp-daemon"
  [cep]="redhat-cne/cloud-event-proxy"
)

# resolve_branch_spec <component> <spec>
# Sets RESOLVED_REPO and RESOLVED_BRANCH
resolve_branch_spec() {
  local component="$1"
  local spec="$2"
  local slash_count
  slash_count=$(echo "$spec" | tr -cd '/' | wc -c)

  if [[ "$slash_count" -eq 1 ]]; then
    local direction="${spec%%/*}"
    RESOLVED_BRANCH="${spec#*/}"
    case "$direction" in
      upstream)
        RESOLVED_REPO="${UPSTREAM_REPOS[$component]}"
        ;;
      downstream)
        RESOLVED_REPO="${DOWNSTREAM_REPOS[$component]}"
        ;;
      *)
        echo "Error: unknown direction '$direction' for $component. Use 'upstream', 'downstream', or org/repo/branch."
        exit 1
        ;;
    esac
  elif [[ "$slash_count" -ge 2 ]]; then
    local org repo
    org="$(echo "$spec" | cut -d/ -f1)"
    repo="$(echo "$spec" | cut -d/ -f2)"
    RESOLVED_BRANCH="$(echo "$spec" | cut -d/ -f3-)"
    RESOLVED_REPO="${org}/${repo}"
  else
    echo "Error: invalid branch spec '$spec'. Expected upstream/branch, downstream/branch, or org/repo/branch."
    exit 1
  fi
}

# is_commit_id <string>
# Returns 0 if the string looks like a git commit hash (7-40 hex chars).
is_commit_id() {
  [[ "$1" =~ ^[0-9a-fA-F]{7,40}$ ]]
}

# git_clone_cmd <repo_url> <branch_or_commit> <dest>
# Emits Dockerfile RUN line(s): clone + optional checkout for commit IDs.
git_clone_cmd() {
  local url="$1" ref="$2" dest="$3"
  if is_commit_id "$ref"; then
    echo "RUN git clone ${url} ${dest} && cd ${dest} && git checkout ${ref}"
  else
    echo "RUN git clone -b ${ref} ${url} ${dest}"
  fi
}

# --- Resolve all specs ---

PTPOP_REPO="" ; PTPOP_BRANCH=""
LPTPD_REPO="" ; LPTPD_BRANCH=""
CEP_REPO=""   ; CEP_BRANCH=""

if [[ -n "$PTPOP_SPEC" ]]; then
  resolve_branch_spec ptpop "$PTPOP_SPEC"
  PTPOP_REPO="$RESOLVED_REPO"; PTPOP_BRANCH="$RESOLVED_BRANCH"
  TARGETS+=(ptpop)
fi

if [[ -n "$LPTPD_SPEC" ]]; then
  resolve_branch_spec lptpd "$LPTPD_SPEC"
  LPTPD_REPO="$RESOLVED_REPO"; LPTPD_BRANCH="$RESOLVED_BRANCH"
  TARGETS+=(lptpd)
fi

if [[ -n "$CEP_SPEC" ]]; then
  resolve_branch_spec cep "$CEP_SPEC"
  CEP_REPO="$RESOLVED_REPO"; CEP_BRANCH="$RESOLVED_BRANCH"
  TARGETS+=(cep)
fi

PHASES=""
$DO_BUILD  && PHASES+="build "
$DO_PUSH   && PHASES+="push "
$DO_DEPLOY && PHASES+="deploy "
$DO_CHECK  && PHASES+="check "

echo "============================================"
echo "Build/Push/Deploy Configuration"
echo "============================================"
[[ -n "$PTPOP_SPEC" ]] && { is_commit_id "$PTPOP_BRANCH" && echo "  ptpop:  https://github.com/${PTPOP_REPO}.git @ commit ${PTPOP_BRANCH}" || echo "  ptpop:  https://github.com/${PTPOP_REPO}.git @ ${PTPOP_BRANCH}"; }
[[ -n "$LPTPD_SPEC" ]] && { is_commit_id "$LPTPD_BRANCH" && echo "  lptpd:  https://github.com/${LPTPD_REPO}.git @ commit ${LPTPD_BRANCH}" || echo "  lptpd:  https://github.com/${LPTPD_REPO}.git @ ${LPTPD_BRANCH}"; }
[[ -n "$CEP_SPEC" ]]   && { is_commit_id "$CEP_BRANCH" && echo "  cep:    https://github.com/${CEP_REPO}.git @ commit ${CEP_BRANCH}" || echo "  cep:    https://github.com/${CEP_REPO}.git @ ${CEP_BRANCH}"; }
[[ -n "$LINUXPTP_RPM_FILENAME" ]] && echo "  linuxptp RPM: ${LINUXPTP_RPM_FILENAME}"
echo "  IMG_PREFIX: ${IMG_PREFIX:-<from Makefile default>}"
echo "  Targets: ${TARGETS[*]}"
echo "  Phases: ${PHASES}"
echo "============================================"
echo ""

# --- Generate temporary Dockerfiles ---

BACKUP_DIR=$(mktemp -d)
trap 'echo "Restoring original Dockerfiles..."; \
  for f in ptpop lptpd cep; do \
    if [[ -f "${BACKUP_DIR}/Dockerfile.${f}" ]]; then \
      cp "${BACKUP_DIR}/Dockerfile.${f}" "${TOOLS_DIR}/Dockerfile.${f}"; \
    fi; \
  done; \
  rm -rf "$BACKUP_DIR"' EXIT

for f in "${TARGETS[@]}"; do
  cp "${TOOLS_DIR}/Dockerfile.${f}" "${BACKUP_DIR}/Dockerfile.${f}"
done

if [[ -n "$PTPOP_SPEC" ]]; then
  PTPOP_CLONE=$(git_clone_cmd "https://github.com/${PTPOP_REPO}.git" "${PTPOP_BRANCH}" "/go/src/github.com/k8snetworkplumbingwg/ptp-operator")
  cat > "${TOOLS_DIR}/Dockerfile.ptpop" <<DOCKERFILE
FROM docker.io/golang:1.26.0 AS builder
RUN apt-get update && apt-get install -y binutils-gold && rm -rf /var/lib/apt/lists/*
WORKDIR /go/src/github.com/k8snetworkplumbingwg/ptp-operator
${PTPOP_CLONE}
ENV GO111MODULE=off
ENV GOMAXPROCS=16

RUN make -j 16

FROM quay.io/centos/centos:stream9
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/ptp-operator/build/_output/bin/ptp-operator /usr/local/bin/
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/ptp-operator/manifests /manifests
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/ptp-operator/bindata /bindata

LABEL io.k8s.display-name="OpenShift ptp-operator" \\
      io.k8s.description="This is a component that manages cluster PTP configuration." \\
      io.openshift.tags="openshift,ptp" \\
      com.redhat.delivery.appregistry=true \\
      maintainer="PTP Dev Team <ptp-dev@redhat.com>"

ENTRYPOINT ["/usr/local/bin/ptp-operator"]
DOCKERFILE
  echo "  ptpop: ${PTPOP_CLONE#RUN }"
fi

if [[ -n "$LPTPD_SPEC" ]]; then
  LPTPD_DEST="/go/src/github.com/k8snetworkplumbingwg/linuxptp-daemon"
  LPTPD_CLONE=$(git_clone_cmd "https://github.com/${LPTPD_REPO}.git" "${LPTPD_BRANCH}" "${LPTPD_DEST}")
  LPTPD_CLONE_SED="${LPTPD_CLONE//&/\\&}"
  sed "s|RUN git clone -b [^ ]* https://github.com/[^ ]*  *${LPTPD_DEST}|${LPTPD_CLONE_SED}|" \
    "${BACKUP_DIR}/Dockerfile.lptpd" > "${TOOLS_DIR}/Dockerfile.lptpd"
  echo "  lptpd: ${LPTPD_CLONE#RUN }"
fi

if [[ -n "$CEP_SPEC" ]]; then
  CEP_DEST="/go/src/github.com/redhat-cne/cloud-event-proxy"
  CEP_CLONE=$(git_clone_cmd "https://github.com/${CEP_REPO}.git" "${CEP_BRANCH}" "${CEP_DEST}")
  CEP_CLONE_SED="${CEP_CLONE//&/\\&}"
  sed "s|RUN git clone -b [^ ]* https://github.com/[^ ]*  *${CEP_DEST}|${CEP_CLONE_SED}|" \
    "${BACKUP_DIR}/Dockerfile.cep" > "${TOOLS_DIR}/Dockerfile.cep"
  echo "  cep:   ${CEP_CLONE#RUN }"
fi

echo ""

# --- Copy custom RPM into build context if provided ---

if [[ -n "$LINUXPTP_RPM_PATH" ]]; then
  mkdir -p "${TOOLS_DIR}/extra"
  cp "$LINUXPTP_RPM_PATH" "${TOOLS_DIR}/extra/${LINUXPTP_RPM_FILENAME}"
  echo "  Copied ${LINUXPTP_RPM_FILENAME} to ptp-tools/extra/"
fi

# --- Build ---

if $DO_BUILD; then
  echo "=== Building images ==="
  for target in "${TARGETS[@]}"; do
    echo "--- Building ${target} ---"
    if [[ "$target" == "lptpd" && -n "$LINUXPTP_RPM_FILENAME" ]]; then
      make -C "$TOOLS_DIR" "podman-build-${target}" \
        "PODMAN_BUILD_ARGS=--build-arg=LINUXPTP_RPM=${LINUXPTP_RPM_FILENAME}"
    else
      make -C "$TOOLS_DIR" "podman-build-${target}"
    fi
  done
fi

# --- Push ---

if $DO_PUSH; then
  echo ""
  echo "=== Pushing images ==="
  for target in "${TARGETS[@]}"; do
    echo "--- Pushing ${target} ---"
    echo "  Deleting old remote tag to force new manifest digest..."
    skopeo delete "docker://${IMG_PREFIX}:${target}" 2>/dev/null || true
    make -C "$TOOLS_DIR" "podman-push-${target}"
  done
fi

# --- Deploy ---

if $DO_DEPLOY; then
  echo ""
  echo "=== Deploying ==="
  make -C "$TOOLS_DIR" deploy-all

  if [[ -n "$PTPOP_SPEC" ]]; then
    echo ""
    echo "=== Restarting operator to pick up new image ==="
    kubectl rollout restart deployment/ptp-operator -n openshift-ptp
    kubectl rollout status deployment/ptp-operator -n openshift-ptp --timeout=120s
  fi

  echo ""
  echo "=== Waiting for operator to reconcile daemonset ==="
  for i in $(seq 1 30); do
    kubectl get daemonset linuxptp-daemon -n openshift-ptp &>/dev/null && break
    sleep 2
  done

  echo "=== Scaling down operator to prevent reconcile conflicts ==="
  kubectl scale deployment ptp-operator -n openshift-ptp --replicas=0
  sleep 5

  echo "=== Patching daemonset pull policies to Always ==="
  PATCH='{"spec":{"template":{"spec":{"containers":['
  PATCH+='{"name":"cloud-event-proxy","imagePullPolicy":"Always"},'
  PATCH+='{"name":"kube-rbac-proxy","imagePullPolicy":"Always"},'
  PATCH+='{"name":"linuxptp-daemon-container","imagePullPolicy":"Always"}'
  PATCH+=']}}}}'
  kubectl patch daemonset linuxptp-daemon -n openshift-ptp --type=strategic -p "$PATCH"

  echo "=== Restarting daemonset to force image pull ==="
  kubectl rollout restart daemonset/linuxptp-daemon -n openshift-ptp
  kubectl rollout status daemonset/linuxptp-daemon -n openshift-ptp --timeout=180s

  echo "=== Scaling operator back up ==="
  kubectl scale deployment ptp-operator -n openshift-ptp --replicas=1
  kubectl rollout status deployment/ptp-operator -n openshift-ptp --timeout=120s

  echo "=== Re-patching pull policies after operator reconcile ==="
  sleep 15
  kubectl patch daemonset linuxptp-daemon -n openshift-ptp --type=strategic -p "$PATCH" || true
  kubectl rollout restart daemonset/linuxptp-daemon -n openshift-ptp
  kubectl rollout status daemonset/linuxptp-daemon -n openshift-ptp --timeout=180s
fi

# --- Check ---

if $DO_CHECK; then
  echo ""
  echo "=== Verifying running image commits ==="
  NAMESPACE="openshift-ptp"
  POD=$(kubectl get pod -n "$NAMESPACE" -l app=linuxptp-daemon -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
  if [[ -z "$POD" ]]; then
    echo "ERROR: no linuxptp-daemon pod found"
    exit 1
  fi

  CHECK_FAILED=false

  if [[ -n "$LPTPD_SPEC" ]]; then
    RUNNING_LPTPD=$(kubectl exec -n "$NAMESPACE" "$POD" -c linuxptp-daemon-container -- ptp --version 2>&1 | grep "Git commit" | head -1 | awk '{print $NF}' || true)
    if is_commit_id "$LPTPD_BRANCH"; then
      EXPECTED_LPTPD="$LPTPD_BRANCH"
    else
      EXPECTED_LPTPD=$(git ls-remote "https://github.com/${LPTPD_REPO}.git" "refs/heads/${LPTPD_BRANCH}" 2>/dev/null | cut -f1 || true)
    fi
    if [[ "$RUNNING_LPTPD" == "$EXPECTED_LPTPD" ]]; then
      echo "  lptpd:  OK  ${RUNNING_LPTPD:0:12}"
    else
      echo "  lptpd:  MISMATCH"
      echo "    running:  ${RUNNING_LPTPD:-<unknown>}"
      echo "    expected: ${EXPECTED_LPTPD:-<unknown>} (${LPTPD_REPO} @ ${LPTPD_BRANCH})"
      CHECK_FAILED=true
    fi
  fi

  if [[ -n "$CEP_SPEC" ]]; then
    RUNNING_CEP=$(kubectl exec -n "$NAMESPACE" "$POD" -c cloud-event-proxy -- ./cloud-event-proxy --version 2>&1 | grep "Git commit" | head -1 | awk '{print $NF}' || true)
    if is_commit_id "$CEP_BRANCH"; then
      EXPECTED_CEP="$CEP_BRANCH"
    else
      EXPECTED_CEP=$(git ls-remote "https://github.com/${CEP_REPO}.git" "refs/heads/${CEP_BRANCH}" 2>/dev/null | cut -f1 || true)
    fi
    if [[ "$RUNNING_CEP" == "$EXPECTED_CEP" ]]; then
      echo "  cep:    OK  ${RUNNING_CEP:0:12}"
    else
      echo "  cep:    MISMATCH"
      echo "    running:  ${RUNNING_CEP:-<unknown>}"
      echo "    expected: ${EXPECTED_CEP:-<unknown>} (${CEP_REPO} @ ${CEP_BRANCH})"
      CHECK_FAILED=true
    fi
  fi

  if [[ -n "$LINUXPTP_RPM_FILENAME" ]]; then
    RUNNING_RPM=$(kubectl exec -n "$NAMESPACE" "$POD" -c linuxptp-daemon-container -- rpm -qa linuxptp 2>/dev/null || true)
    echo "  linuxptp RPM: ${RUNNING_RPM:-<unknown>}"
  fi

  echo ""
  echo "  Pull policies:"
  kubectl get pod "$POD" -n "$NAMESPACE" -o jsonpath='{range .spec.containers[*]}    {.name}: {.imagePullPolicy}{"\n"}{end}'

  if $CHECK_FAILED; then
    echo ""
    echo "  VERIFICATION FAILED: one or more images do not match expected commits."
    exit 1
  else
    echo ""
    echo "  All running commits match expected branch HEADs."
  fi
fi

echo ""
echo "============================================"
echo "Done. Phases completed: ${PHASES}"
echo "============================================"
