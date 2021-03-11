REPO_DIR="$(dirname $0)/.."
NAMESPACE=openshift-ptp
OPERATOR_EXEC=oc

export RELEASE_VERSION=v4.8.0
export IMAGE_TAG=latest
export OPERATOR_NAME=ptp-operator

LINUXPTP_DAEMON_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-ptp | jq --raw-output '.Digest')
PTP_OPERATOR_IMAGE_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-ptp-operator | jq --raw-output '.Digest')
KUBE_RBAC_PROXY_DIGEST=$(skopeo inspect docker://quay.io/openshift/origin-kube-rbac-proxy | jq --raw-output '.Digest')
export LINUXPTP_DAEMON_IMAGE=${LINUXPTP_DAEMON_IMAGE:-quay.io/openshift/origin-ptp@${LINUXPTP_DAEMON_IMAGE_DIGEST}}
export PTP_OPERATOR_IMAGE=${PTP_OPERATOR_IMAGE:-quay.io/openshift/origin-ptp-operator@${PTP_OPERATOR_IMAGE_DIGEST}}
export KUBE_RBAC_PROXY_IMAGE=${KUBE_RBAC_PROXY_IMAGE:-quay.io/openshift/origin-kube-rbac-proxy@${KUBE_RBAC_PROXY_DIGEST}}
