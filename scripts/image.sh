#!/bin/sh
set -x
VERSION=latest
IMAGE_TAG=ptp-operator-test
REPO=quay.io/redhat-cne
make buildtest
docker build -t ${IMAGE_TAG} --rm -f Dockerfile.conformancetests .

docker tag ${IMAGE_TAG} ${REPO}/${IMAGE_TAG}:${VERSION}
docker push ${REPO}/${IMAGE_TAG}:${VERSION}
