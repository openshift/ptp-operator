#!/bin/bash

CTRL=kubectl
CONFIG_MAP_DIR="linuxptp-configmap"
mkdir -p $CONFIG_MAP_DIR

nodes=$($CTRL get nodes -o jsonpath="{.items[*].metadata.name}")
array=(`echo $nodes | sed 's/ /\n/g'`)
for n in "${array[@]}"
do
	echo $n
	touch "$CONFIG_MAP_DIR/$n"
	echo '{"interface":"eth0", "ptp4lOpts":"-s -2", "phc2sysOpts":"-a -r"}' > "$CONFIG_MAP_DIR/$n"
done

$CTRL delete configmap linuxptp-configmap -n openshift-ptp || true
$CTRL create configmap linuxptp-configmap --from-file=$CONFIG_MAP_DIR -n openshift-ptp
