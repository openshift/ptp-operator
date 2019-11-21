REPO_DIR="$(dirname $0)/.."
NAMESPACE=openshift-ptp
OPERATOR_EXEC=oc

export RELEASE_VERSION=v4.3.0
export OPERATOR_NAME=ptp-operator
IMAGE_REPOSITORY="https://quay.io/api/v1/repository/openshift/origin-ptp"

if [ ! -x "$(command -v jq)" ]; then
  echo "Could not find jq in the current PATH"
  exit 1
fi

LINUXPTP_DAEMON_IMAGE_DIGEST=$(curl -s -X GET "${IMAGE_REPOSITORY}/tag/?specificTag=${RELEASE_VERSION//v}&onlyActiveTags=true" | jq -r .tags[0].manifest_digest)
export LINUXPTP_DAEMON_IMAGE=${LINUXPTP_DAEMON_IMAGE:-quay.io/openshift/origin-ptp@${LINUXPTP_DAEMON_IMAGE_DIGEST}}
