#!/bin/bash
# Run unit tests and generate filtered coverage profile.
set -e
go test $(go list ./... | grep -v '/test/') -coverprofile=coverage.raw.out
grep -vE "zz_generated|mock_|/test/|vendor/|/bin/" coverage.raw.out > coverage.out
