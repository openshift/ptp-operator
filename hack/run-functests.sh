#!/bin/bash
set -x
which ginkgo

if [ $? -ne 0 ]; then
# we are moving to a temp folder as in go.mod we have a dependency that is not
# resolved if we are not using google's GOPROXY. That is not the case when building as
# we are using vendored dependencies
	GINKGO_TMP_DIR=$(mktemp -d)
	cd $GINKGO_TMP_DIR
	go mod init tmp
	GOFLAGS=-mod=mod go install github.com/onsi/ginkgo/v2/ginkgo@v2.9.5
	rm -rf $GINKGO_TMP_DIR
	echo "Downloading ginkgo tool"
	cd -
fi

GOPATH="${GOPATH:-~/go}"
JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
export PATH=$PATH:$GOPATH/bin

VALIDATION_SUIT_SUBSTR="validation"

if [[ $SUITE == *"$VALIDATION_SUIT_SUBSTR"* ]]
then
	GOFLAGS=-mod=vendor ginkgo --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v -p "$SUITE"
else
	GOFLAGS=-mod=vendor ginkgo -keepGoing --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v -p "$SUITE"/serial "$SUITE"/parallel
fi