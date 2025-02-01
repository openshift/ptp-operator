#!/bin/bash

vendor/k8s.io/code-generator/generate-groups.sh client,lister,informer \
	github.com/k8snetworkplumbingwg/ptp-operator/pkg/client \
	github.com/k8snetworkplumbingwg/ptp-operator/pkg/apis \
	ptp:v1
