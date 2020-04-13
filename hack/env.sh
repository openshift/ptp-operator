REPO_DIR="$(dirname $0)/.."
NAMESPACE=openshift-ptp
OPERATOR_EXEC=oc

export RELEASE_VERSION=v4.3.0
export IMAGE_TAG=4.3
export OPERATOR_NAME=ptp-operator

# This will find the latest digest.
LINUXPTP_DAEMON_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-ptp | jq --raw-output '.Digest')
PTP_OPERATOR_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-ptp-operator | jq --raw-output '.Digest')
export LINUXPTP_DAEMON_IMAGE=${LINUXPTP_DAEMON_IMAGE:-quay.io/openshift/origin-ptp:${IMAGE_TAG}}
export PTP_OPERATOR_IMAGE=${PTP_OPERATOR_IMAGE:-quay.io/openshift/origin-ptp-operator:${IMAGE_TAG}}
