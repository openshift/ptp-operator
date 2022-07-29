#!/bin/bash
set -x
IMAGE=quay.io/redhat-cne/ptp-operator-test:latest
KUBECONFIG=$1
TEST_MODE=$2
OUTPUT=$3
ENABLE_TC=$4

docker run -v $OUTPUT_LOC:/tmp:Z $TNF_IMAGE 
docker run -e PTP_TEST_MODE=$TEST_MODE -e ENABLE_TEST_CASE=$ENABLE_TC -v $KUBECONFIG:/tmp/config:Z -v $OUTPUT:/output:Z  $IMAGE
