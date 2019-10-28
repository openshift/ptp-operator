#!/bin/bash
source hack/env.sh

pushd ${REPO_DIR}/deploy
	FILES="operator.yaml
		service_account.yaml
		clusterrolebinding.yaml
		clusterrole.yaml
		crds/ptp.openshift.io_nodeptpdevices_crd.yaml
		crds/ptp.openshift.io_ptpoperatorconfigs_crd.yaml
		crds/ptp.openshift.io_ptpconfigs_crd.yaml"

	for f in ${FILES}; do
		envsubst< ${f} | ${OPERATOR_EXEC} delete --ignore-not-found -n ${NAMESPACE} -f -
	done
popd
