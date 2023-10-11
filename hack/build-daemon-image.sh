#!/bin/bash

docker build -t ghcr.io/k8snetworkplumbingwg/linuxptp-daemon:latest -f ./Dockerfile.daemon .
