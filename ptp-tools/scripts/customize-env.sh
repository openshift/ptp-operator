#!/bin/bash
IMG_PREFIX=$1
ENV_PATH=$2

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
          env:
            - name: OPERATOR_NAME
              value: "ptp-operator"
            - name: RELEASE_VERSION
              value: "v4.19.0"
            - name: LINUXPTP_DAEMON_IMAGE
              value: "$IMG_PREFIX:lptpd"
            - name: KUBE_RBAC_PROXY_IMAGE
              value: "$IMG_PREFIX:krp"
            - name: SIDECAR_EVENT_IMAGE
              value: "$IMG_PREFIX:cep"
EOF

sed -i 's/IfNotPresent/Always/g' $ENV_PATH/manager.yaml