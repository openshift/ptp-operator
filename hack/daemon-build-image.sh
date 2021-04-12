#!/bin/bash

podman build -t openshift.io/linuxptp-daemon -f ./cmd/linuxptp-daemon/Dockerfile .
