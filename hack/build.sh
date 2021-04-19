#!/usr/bin/env bash

set -eu

REPO=github.com/openshift/ptp-operator
WHAT=${WHAT:-manager}
GOFLAGS=${GOFLAGS:-}
GLDFLAGS=${GLDFLAGS:-}

GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)

# Go to the root of the repo
cdup="$(git rev-parse --show-cdup)" && test -n "$cdup" && cd "$cdup"

if [ -z ${VERSION_OVERRIDE+a} ]; then
	echo "Using version from git..."
	VERSION_OVERRIDE=$(git describe --abbrev=8 --dirty --always)
fi

GLDFLAGS+="-X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"

export BIN_PATH=build/_output/bin/
export BIN_NAME=ptp-operator
mkdir -p ${BIN_PATH}

CGO_ENABLED=1

echo "Building ${REPO}/cmd/${WHAT} (${VERSION_OVERRIDE})"
CGO_ENABLED=${CGO_ENABLED} GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS} -s -w" -o ${BIN_PATH}/${BIN_NAME} ${REPO}/cmd/${WHAT}
