REPO_DIR="$(dirname $0)/.."
NAMESPACE=openshift-ptp
OPERATOR_EXEC=oc

export RELEASE_VERSION=v4.3.0
export OPERATOR_NAME=ptp-operator

LINUXPTP_DAEMON_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-ptp | jq --raw-output '.Digest')
export LINUXPTP_DAEMON_IMAGE=${LINUXPTP_DAEMON_IMAGE:-quay.io/openshift/origin-ptp@${LINUX_DAEMON_IMAGE_DIGEST}}
