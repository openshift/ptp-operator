#!/bin/bash

ORG_PATH="github.com/k8snetworkplumbingwg"
REPO_PATH="${ORG_PATH}/ptp-operator"

if [ ! -h .gopath/src/${REPO_PATH} ]; then
        mkdir -p .gopath/src/${ORG_PATH}
        ln -s ../../../.. .gopath/src/${REPO_PATH} || exit 255
fi

export CGO_ENABLED=0
export GO15VENDOREXPERIMENT=1
export GOBIN=${PWD}/bin
export GOPATH=${PWD}/.gopath
go build --mod=vendor -tags no_openssl "$@" -o bin/linuxptp-daemon ${REPO_PATH}/pkg/daemon/cmd
