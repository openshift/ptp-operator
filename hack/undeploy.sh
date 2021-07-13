#!/bin/bash
source hack/env.sh

pushd ${REPO_DIR}/deploy
	FILES="operator.yaml
		service_account.yaml
		clusterrolebinding.yaml
		clusterrole.yaml
		role.yaml
		rolebinding.yaml
		../config/crd/bases/ptp.openshift.io_nodeptpdevices.yaml
		../config/crd/bases/ptp.openshift.io_ptpoperatorconfigs.yaml
		../config/crd/bases/ptp.openshift.io_ptpconfigs.yaml"

	for f in ${FILES}; do
		envsubst< ${f} | ${OPERATOR_EXEC} delete --ignore-not-found -n ${NAMESPACE} -f -
	done
popd
