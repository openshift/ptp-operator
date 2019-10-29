#!/bin/bash

vendor/k8s.io/code-generator/generate-groups.sh client,lister,informer \
	github.com/openshift/ptp-operator/pkg/client \
	github.com/openshift/ptp-operator/pkg/apis \
	ptp:v1
