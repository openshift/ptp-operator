#!/bin/bash
# Compare test coverage of the current branch against a base ref.
# Usage: hack/coverage-gate.sh [base-ref]
# When base-ref is omitted, auto-detects the local branch tracking
# the upstream k8snetworkplumbingwg remote's default branch.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

resolve_base_ref() {
  if [ -n "$1" ]; then
    echo "$1"
    return
  fi

  # Find the remote pointing to k8snetworkplumbingwg
  UPSTREAM_REMOTE=$(git remote -v | grep 'k8snetworkplumbingwg/.*fetch' | awk '{print $1}' | head -1)
  if [ -z "${UPSTREAM_REMOTE}" ]; then
    echo "main"
    return
  fi

  git fetch "${UPSTREAM_REMOTE}" --quiet

  # Find the default branch of the upstream remote
  UPSTREAM_HEAD=$(git remote show "${UPSTREAM_REMOTE}" | sed -n '/HEAD branch/s/.*: //p')
  UPSTREAM_REF="${UPSTREAM_REMOTE}/${UPSTREAM_HEAD}"

  # Find or create a local branch tracking this remote branch
  LOCAL_BRANCH=$(git branch --list --format='%(refname:short) %(upstream:short)' | grep " ${UPSTREAM_REF}$" | awk '{print $1}' | head -1)
  if [ -z "${LOCAL_BRANCH}" ]; then
    LOCAL_BRANCH="up-${UPSTREAM_HEAD}"
    echo "==> Creating tracking branch '${LOCAL_BRANCH}' for '${UPSTREAM_REF}'..." >&2
    git branch "${LOCAL_BRANCH}" "${UPSTREAM_REF}" --quiet
    git branch --set-upstream-to="${UPSTREAM_REF}" "${LOCAL_BRANCH}" --quiet >/dev/null
  fi

  # Update tracking branch to latest without checkout
  git branch -f "${LOCAL_BRANCH}" "${UPSTREAM_REF}" --quiet

  echo "${LOCAL_BRANCH}"
}

BASE_REF=$(resolve_base_ref "$1")

echo "==> Running tests on current branch..."
"${SCRIPT_DIR}/unit-test.sh"
CURRENT_COV=$(go tool cover -func=coverage.out | grep ^total | awk '{print $3}' | tr -d '%')

echo "==> Running tests on base ref '${BASE_REF}'..."
CURRENT_BRANCH=$(git symbolic-ref --short HEAD 2>/dev/null || git rev-parse HEAD)
TMPTEST=$(mktemp)
cp "${SCRIPT_DIR}/unit-test.sh" "${TMPTEST}"
STASHED=false
if ! git diff --quiet || ! git diff --cached --quiet; then
  git stash --quiet
  STASHED=true
fi
git checkout "${BASE_REF}" --quiet
bash "${TMPTEST}"
rm -f "${TMPTEST}"
BASE_COV=$(go tool cover -func=coverage.out | grep ^total | awk '{print $3}' | tr -d '%')
git checkout "${CURRENT_BRANCH}" --quiet
if [ "${STASHED}" = true ]; then
  git stash pop --quiet
fi

DIFF=$(echo "${CURRENT_COV} - ${BASE_COV}" | bc)
echo ""
echo "Base coverage (${BASE_REF}): ${BASE_COV}%"
echo "Current coverage:            ${CURRENT_COV}%"
echo "Difference:                  ${DIFF}%"

if (( $(echo "${DIFF} < 0" | bc -l) )); then
  echo "❌ FAIL: Coverage decreased by ${DIFF}%"
  exit 1
elif (( $(echo "${DIFF} > 0" | bc -l) )); then
  echo "🎉 Coverage increased by ${DIFF}%, good job!"
else
  echo "✅ Coverage unchanged."
fi
