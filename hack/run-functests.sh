#!/bin/bash
set -x
which ginkgo
ginkgo version
if [ $? -ne 0 ]; then
# we are moving to a temp folder as in go.mod we have a dependency that is not
# resolved if we are not using google's GOPROXY. That is not the case when building as
# we are using vendored dependencies
	GINKGO_TMP_DIR=$(mktemp -d)
	cd $GINKGO_TMP_DIR
	go mod init tmp
	GOFLAGS=-mod=mod go install github.com/onsi/ginkgo/v2/ginkgo@latest
	rm -rf $GINKGO_TMP_DIR
	echo "Downloading ginkgo tool"
	cd -
fi

GOPATH="${GOPATH:-~/go}"
JUNIT_OUTPUT="${JUNIT_OUTPUT:-/tmp/artifacts/unit_report.xml}"
export PATH=$PATH:$GOPATH/bin

# GOFLAGS=-mod=vendor ginkgo -v -p "$SUITE"/serial  "$SUITE"/parallel  --junit-report=$JUNIT_OUTPUT
GOFLAGS=-mod=vendor ginkgo -v -p "$SUITE"/serial  --junit-report=$JUNIT_OUTPUT