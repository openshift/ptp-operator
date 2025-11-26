#!/bin/bash

if [[ $1 == "-h" || $1 == "--help" || -z $CATALOG_IMG ]]; then
  echo "Usage:"
  echo "  $(basename "$0") quay.io/repo/ptp-operator-catalog:tag"
  echo "  $(basename "$0") --remove|--rm"
  exit 1
fi

if [[ $1 == "--remove" || $1 == "--rm" ]]; then
  action="Removing"
  verb="delete"
  CATALOG_IMG="any"
else
  action="Installing"
  verb="apply"
  CATALOG_IMG=$1
fi

echo "$action OLMv1 ClusterCatalog:"
oc "$verb" -f - <<EOF
apiVersion: olm.operatorframework.io/v1
kind: ClusterCatalog
metadata:
  name: ptp-operator-catalog
  namespace: openshift-marketplace
spec:
  priority: 1000
  source:
    type: Image
    image:
      ref: ${CATALOG_IMG}
EOF

echo "$action OLMv0 CatalogSource:"
oc "$verb" -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: ptp-operator-catalog
  namespace: openshift-marketplace
spec:
  sourceType: image
  displayName: Custom PTP Operator catalog
  publisher: Red Hat (dev)
  image: ${CATALOG_IMG}
EOF
