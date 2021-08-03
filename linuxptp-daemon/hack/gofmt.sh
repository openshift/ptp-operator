#!/bin/bash

gofmt_command="gofmt -w -s -l `find . -path ./vendor -prune -o -type f -name '*.go' -print`"
res=$(eval ${gofmt_command})
if [[ -z ${res} ]]; then
	echo "INFO: gofmt success"
	exit 0
else
	echo ${res}
	echo "ERROR: gofmt reported formatting issues, run 'make fmt'"
	exit 1
fi
