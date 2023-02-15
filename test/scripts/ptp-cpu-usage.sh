#!/bin/bash
#
# Simple bash tool for developers to get the cpu usage of PTP containers. This tool
# is not intented for use in any production/release system. It needs a previously
# exported KUBECONFIG env var.
#
# This script polls an openshift cluster with the "curl" tool to get the cpu usage
# of each PTP container found in a cluster. The cpu usage comes from the prometheus metric
# container_cpu_usage_total_seconds, using the rate() function on samples from the
# last N seconds. This time window is configurable via env var. Each container cpu usage
# value will be sent to stdout and to a csv file (default ptp-cpu-usage.csv) which is
# removed at the begining in case it already exists.
#
# Optional env vars and default values (if env vars not exported):
#   PTP_CPU_USAGE_DURATION_MINS         = 5
#   PTP_CPU_USAGE_POLLING_INTERVAL_SECS = 10
#   PTP_CPU_USAGE_PROM_RATE_WINDOW      = 60s -> Values allowed as in prometheus queries (83s, 1m30s, 3m, 4h...)
#   PTP_CPU_USAGE_OUTPUT_FILE           = "ptp-cpu-usage.csv"
#
# Usage examples:
#   a) No env vars (except KUBECONFIG already exported):
#    ./ptp-cpu-usage.sh
#       Polls every 10 seconds during 5 minutes.
#       Prometheus time window for rate() is 60s.
#       Creates csv file called "ptp-cpu-usage.csv"
#
#   b) PTP_CPU_USAGE_DURATION_MINS=10 PTP_CPU_USAGE_POLLING_INTERVAL_SECS=30 PTP_CPU_USAGE_PROM_RATE_WINDOW=5m PTP_CPU_USAGE_OUTPUT_FILE=myfile.csv ./ptp-cpu-usage.sh
#       Polls every 30 seconds during 10 mins.
#       Prometheus time window for rate() is 5m.
#       Creates csv file called "myfile.csv".
#
# Dependencies:
#  - oc client
#  - jq
#  - curl
#  - tee
#  - KUBECONFIG env var must be exported.
#
# Procedure:
#  1. Creates a port-forward to the first pod of the prometheus statefulset. This will be
#     automatically removed at then (except if script is killed with ctrl+c).
#  2. Makes a list of all the ptp pods (operator+daemonset) in a cluster.
#  3. Gets each container inside each ptp pod and makes a map pod->containers.
#  4. Starts polling each pod's container with curl to get the corresponding cpu
#     usage through the prometheus metric.
#     Each container cpu usage is shown in the stdout as a comma-separated values line, with
#     the value in millicores being the last field. Also, each line is sent to a file in the
#     current folder called "ptp-cpu-usage.csv".
#     The csv line format is:
#        timestamp in string format, timestamp since epoch in seconds, pod, container, cpu_usage (in milliCores)
#        Example:
#          Wed Feb 15 11:55:57 AM UTC 2023,1676462157,linuxptp-daemon-74m2g,cloud-event-proxy,2.302596545833069
#          Wed Feb 15 11:55:57 AM UTC 2023,1676462157,linuxptp-daemon-74m2g,kube-rbac-proxy,0.0964516208333303
#          Wed Feb 15 11:55:57 AM UTC 2023,1676462157,linuxptp-daemon-74m2g,linuxptp-daemon-container,13.541781749999396
#          Wed Feb 15 11:55:57 AM UTC 2023,1676462157,ptp-operator-5f4f48d7c-x7zkf,ptp-operator,0.91628999166673
#
# Troubleshooting:
#   If no samples are shown in stdout, probably the prometheus rate() is not returning
#   any value. This is typically due to rate() not having enought samples to work (2).
#   In this cases, the recommended workaround is increasing the prometheus time window
#   with env var PTP_CPU_USAGE_PROM_RATE_WINDOW to values around 5m.
#


polling_interval="${PTP_CPU_USAGE_POLLING_INTERVAL_SECS:-10}"
duration_mins="${PTP_CPU_USAGE_DURATION_MINS:-5}"
prom_rate_window="${PTP_CPU_USAGE_PROM_RATE_WINDOW:-60s}"
output_file="${PTP_CPU_USAGE_OUTPUT_FILE:-ptp-cpu-usage.csv}"


# Helper function to get the pod's containers names.
function get_pod_containers() {
    local pod_name=$1
    oc get pods -n openshift-ptp $pod -o jsonpath='{range .spec.containers[*]}{.name}{" "}{end}'
}

# Helper function to get the cpu usage from a pod's container.
function get_container_cpu_usage() {
    local pod=$1
    local container=$2
    local cpu_cmd="curl -s http://localhost:9090/api/v1/query --data-urlencode 'query=rate(container_cpu_usage_seconds_total{container=\"$container\", pod=\"$pod\"}[$prom_rate_window])*1000' | jq -r '.data.result[] | .value[1]'"

    # This loop forever makes sure you always get valid results from prometheus rate() query.
    # rate(metric[1m]) may not give any result because there's a couple of seconds every minute
    # where prometheus doesn't have enough samples (2) to be used by the rate function.
    while true; do
        local cpu=$(eval $cpu_cmd)
        if [[ "$cpu" != "" ]]; then
            echo $cpu
            return
        fi
        # Wait one second before querying again.
        sleep 1
    done
}

echo "Script ptp-cpu-usage.sh params:"
echo " - Duration: ${duration_mins} mins."
echo " - Polling Interval: ${polling_interval}"
echo " - Prometheus time window for rate: ${prom_rate_window} -> rate(container_cpu_usage_seconds_total{pod="...", container="..."}[${prom_rate_window}])"
echo " - CSV output file: ${output_file}"

# Create the port forward.
echo
echo "> Creating the prometheus pod port-forward"
oc -n openshift-monitoring port-forward prometheus-k8s-0 9090:9090 --address='0.0.0.0' &>/dev/null &
port_forward_pid=$!
echo "  - Port-forward pid: ${port_forward_pid}"
sleep 2

echo "> Getting the list of all PTP pods' containers"
declare -A containers

ptp_operator_pods=`oc get pods -n openshift-ptp -l name=ptp-operator -o jsonpath='{range .items[*]}{.metadata.name}{" "}{end}'`
for pod in $ptp_operator_pods; do
    containers[${pod}]=$(get_pod_containers)
    echo "  - PTP operator pod $pod containers: ${containers[$pod]}"
done

ptp_daemonset_pods=`oc get pod -n openshift-ptp -l app=linuxptp-daemon -o jsonpath='{range .items[*]}{.metadata.name}{" "}{end}'`
for pod in $ptp_daemonset_pods; do
    containers[${pod}]=$(get_pod_containers)
    echo "  - PTP daemonset pod $pod containers: ${containers[$pod]}"
done

echo "Removing previous csv file ${PTP_CPU_USAGE_OUTPUT_FILE} (if it exists)."
rm ${PTP_CPU_USAGE_OUTPUT_FILE}

# Start fetching values until ctrl+c or PTP_CPU_USAGE_DURATION_MINS (default 2 mins)
echo "> Polling for ${duration_mins} minutes."
echo "  - Start time : `date`"
runtime="${duration_mins} minute"
endtime=$(date -ud "$runtime" +%s)
echo "  - End time   : `date --date=@${endtime}`"

while [[ $(date -u +%s) -le $endtime ]]; do
    polling_date=$(date -u +%s)
    polling_date_str=`date --date=@${polling_date}`
    for pod in "${!containers[@]}"; do
        for container in ${containers[$pod]}; do
            cpu=$(get_container_cpu_usage $pod $container)
            echo "${polling_date_str},${polling_date},$pod,$container,$cpu" | tee -a $output_file
        done
    done
    sleep $polling_interval
done

echo
echo "End. Stopping port-forward..."
kill ${port_forward_pid}
