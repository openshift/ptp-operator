#!/bin/bash
set -x
set -euo pipefail

timeout="$1"
interval="$2"
shift 2
cmd=("$@")

start_time=$(date +%s)

while true; do
    if "${cmd[@]}"; then
        echo "Command succeeded."
        exit 0
    fi
    now=$(date +%s)
    elapsed=$((now - start_time))

    if ((elapsed >= timeout)); then
        echo "Timeout of $timeout seconds reached. Command failed."
        exit 1
    fi

    echo "Command failed. Retrying in $interval seconds..."
    sleep "$interval"
done
