#!/bin/bash
IMG_PREFIX=$1
ENV_PATH=$2

EVENT_PROXY_IMAGE=""
if [[ "${ENABLE_CEPV2:-}" == "true" ]]; then
  EVENT_PROXY_IMAGE="$IMG_PREFIX:cepv2"
fi
LINUXPTP_VERBOSITY="${LINUXPTP_VERBOSITY:-10}"

cat <<EOF > $ENV_PATH/env.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ptp-operator
  namespace: openshift-ptp
spec:
  template:
    spec:
      containers:
        - name: ptp-operator
          imagePullPolicy: Always
          env:
            - name: OPERATOR_NAME
              value: "ptp-operator"
            - name: RELEASE_VERSION
              value: "v5.0.0"
            - name: LINUXPTP_DAEMON_IMAGE
              value: "$IMG_PREFIX:lptpd"
            - name: KUBE_RBAC_PROXY_IMAGE
              value: "$IMG_PREFIX:krp"
            - name: SIDECAR_EVENT_IMAGE
              value: "$IMG_PREFIX:cep"
            - name: EVENT_PROXY_IMAGE
              value: "$EVENT_PROXY_IMAGE"
            - name: LINUXPTP_VERBOSITY
              value: "$LINUXPTP_VERBOSITY"
            - name: IMAGE_PULL_POLICY
              value: "Always"
EOF
