#!/usr/bin/env bash

set -eu

REPO=github.com/k8snetworkplumbingwg/ptp-operator
WHAT=${WHAT:-manager}
GOFLAGS=${GOFLAGS:-}
GLDFLAGS=${GLDFLAGS:-}
CGO_ENABLED=${CGO_ENABLED:-1}

GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)

# Go to the root of the repo (skip if .git is absent, e.g. remote builder)
cdup="$(git rev-parse --show-cdup 2>/dev/null)" && test -n "$cdup" && cd "$cdup"

if [ -z ${VERSION_OVERRIDE+a} ]; then
	if git rev-parse --git-dir >/dev/null 2>&1; then
		echo "Using version from git..."
		VERSION_OVERRIDE=$(git describe --abbrev=8 --dirty --always)
	else
		VERSION_OVERRIDE="unknown"
	fi
fi

GLDFLAGS+="-X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"

export BIN_PATH=build/_output/bin/
export BIN_NAME=ptp-operator
mkdir -p ${BIN_PATH}

echo "Building ${REPO}/cmd/${WHAT} (${VERSION_OVERRIDE})"
CGO_ENABLED=${CGO_ENABLED} CC="gcc -fuse-ld=gold" GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS} -s -w" -o ${BIN_PATH}/${BIN_NAME} ${REPO}
