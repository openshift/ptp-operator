#!/usr/bin/env bash

set -eu

PTP_OPERATOR_PKG="github.com/openshift/ptp-operator"

CMD=${CMD:- "ptp-operator"}
GOFLAGS=${GOFLAGS:-}
GLDFLAGS=${GLDFLAGS:-}

#if [ -z ${VERSION+a} ]; then
#	echo "Using version from git..."
#	VERSION=$(git describe --abbrev=8 --dirty --always)
#fi

#GLDFLAGS+="-X ${PTP_OPERATOR_PKG}/pkg/version.Raw=${VERSION}"

eval $(go env)

echo "Building ${PTP_OPERATOR_PKG}/cmd/${CMD}"
#echo "Building ${PTP_OPERATOR_PKG}/cmd/${CMD} (${VERSION})"
CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS}" -o bin/${CMD} ${PTP_OPERATOR_PKG}/cmd/${CMD}
