#!/bin/bash
# Run unit tests and generate filtered coverage profile.
set -e
go test $(go list ./... | grep -vE '/test/|/hack/validate-crds$') -coverprofile=coverage.raw.out
grep -vE "zz_generated|mock_|/test/|vendor/|/bin/" coverage.raw.out > coverage.out

# Conformance helper packages and gnss-sim are outside the filtered operator module
# graph; run them explicitly so new tests are not silently skipped in CI.
# coverage-gate.sh also runs this script on the PR base ref, where gnss-sim may
# not exist yet — skip gracefully in that case.
go test ./test/pkg/ptphelper/... ./test/pkg/pods/...
# Pure testconfig unit tests (exclude discovery helpers that need a live cluster).
go test ./test/pkg/testconfig/ -run 'TestClockNodeInfo|TestHasGNSSDeviceWrapperFindsSibling|TestPreferDiffNodeSolution|TestStripPhc2sysRealtimeOpts|TestIsTelcoGMMode|TestDirectConnectionTopologySolutions' -count=1
if [ -d ptp-tools/gnss-sim ]; then
	(cd ptp-tools/gnss-sim && go test ./...)
fi
