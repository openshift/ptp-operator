source hack/env.sh

pushd ${REPO_DIR}/deploy
	if ! ${OPERATOR_EXEC} get ns ${NAMESPACE} > /dev/null 2>&1 && test -f namespace.yaml ; then
		envsubst< namespace.yaml | ${OPERATOR_EXEC} apply -f -
	fi

	FILES="crds/ptp.openshift.io_nodeptpdevices_crd.yaml
		crds/ptp.openshift.io_ptpoperatorconfigs_crd.yaml
		crds/ptp.openshift.io_ptpconfigs_crd.yaml
		service_account.yaml
		clusterrole.yaml
		clusterrolebinding.yaml
		operator.yaml"

	for f in ${FILES}; do
		if [ "$(echo ${EXCLUSIONS[@]} | grep -o ${f} | wc -w | xargs)" == "0" ] ; then
			envsubst< ${f} | ${OPERATOR_EXEC} apply -n ${NAMESPACE} --validate=false -f -
		fi
	done
popd
