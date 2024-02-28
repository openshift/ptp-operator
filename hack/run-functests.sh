#!/bin/bash
set -x
# Set go version
pattern="4.[0-9]+"
if [[ "$T5CI_VERSION" =~ $pattern ]]; then
  source $HOME/golang-1.20
else
  # make sure the test runs with specific go vervsion.
  # install go in <ptp-operator-repo>/bin if not already installed
  GO_VERSION=1.20.4
  REPO_BIN_PATH=$(pwd)/bin
  echo "REPO_BIN_PATH is ${REPO_BIN_PATH}"
  export PATH="${REPO_BIN_PATH}/go/bin:$PATH"
  if go version | grep -q "go${GO_VERSION}"; then
    echo "Go ${GO_VERSION} is already installed."
  else
    mkdir -p ${REPO_BIN_PATH}
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
    if uname -a | grep -q "e2e-telco5g"; then
      # if it is on a Prow job, replace the current go version
      rm -rf /usr/local/go
      tar -C /usr/local -xzf "$temp_dir/${GO_BINARY}"
    else
      tar -C ${REPO_BIN_PATH} -xzf "$temp_dir/${GO_BINARY}"
    fi
    rm -rf "$temp_dir"
  fi
fi

which ginkgo
if [ $? -ne 0 ]; then
  # we are moving to a temp folder as in go.mod we have a dependency that is not
  # resolved if we are not using google's GOPROXY. That is not the case when building as
  # we are using vendored dependencies
  GINKGO_TMP_DIR=$(mktemp -d)
  cd $GINKGO_TMP_DIR
  go mod init tmp
  GOFLAGS=-mod=mod go install github.com/onsi/ginkgo/v2/ginkgo@v2.11.0
  rm -rf $GINKGO_TMP_DIR
  echo "Downloading ginkgo tool"
  cd -
fi

ginkgo version

GOPATH="${GOPATH:-~/go}"
JUNIT_OUTPUT_DIR="${JUNIT_OUTPUT_DIR:-/tmp/artifacts}"
JUNIT_OUTPUT_FILE="${JUNIT_OUTPUT_FILE:-unit_report.xml}"
export PATH=$PATH:$GOPATH/bin

VALIDATION_SUIT_SUBSTR="validation"

go version
if [[ $SUITE == *"$VALIDATION_SUIT_SUBSTR"* ]]; then
  GOFLAGS=-mod=vendor ginkgo --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v -p "$SUITE"
else
  GOFLAGS=-mod=vendor ginkgo --keep-going --output-dir=$JUNIT_OUTPUT_DIR --junit-report=$JUNIT_OUTPUT_FILE -v -p "$SUITE"/serial "$SUITE"/parallel
fi
