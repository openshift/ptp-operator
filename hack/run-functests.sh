#!/bin/bash
set -x

# make sure the test runs with specific go vervsion.
# install go in <ptp-operator-repo>/bin if not already installed
GO_VERSION=1.22.4
REPO_BIN_PATH=$(pwd)/bin
echo "REPO_BIN_PATH is ${REPO_BIN_PATH}"
export PATH="${REPO_BIN_PATH}/go/bin:$PATH"
if go version | grep -q "${GO_VERSION}"; then
	echo "Go ${GO_VERSION} is already installed."
else
	# Check the operating system type
	if [[ "$(uname)" == "Darwin" ]]; then
		# macOS
		GO_BINARY="go${GO_VERSION}.darwin-amd64.tar.gz"
	elif [[ "$(uname)" == "Linux" ]]; then
		GO_BINARY="go${GO_VERSION}.linux-amd64.tar.gz"
	else
		echo "Unsupported operating system $(uname)."
		exit 1
	fi
	temp_dir=$(mktemp -d)
	wget https://go.dev/dl/${GO_BINARY} -P "$temp_dir"
	tar -C ${REPO_BIN_PATH} -xzf "$temp_dir/${GO_BINARY}"
	rm -rf "$temp_dir"
fi

which ginkgo

if [ $? -ne 0 ]; then
# we are moving to a temp folder as in go.mod we have a dependency that is not
# resolved if we are not using google's GOPROXY. That is not the case when building as
# we are using vendored dependencies
	GINKGO_TMP_DIR=$(mktemp -d)
	cd $GINKGO_TMP_DIR
	go mod init tmp
	GOFLAGS=-mod=mod go install github.com/onsi/ginkgo/v2/ginkgo@v2.8.4
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