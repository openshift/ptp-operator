package test

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	"github.com/openshift/ptp-operator/test/pkg/client"
	"github.com/openshift/ptp-operator/test/pkg/metrics"
	"github.com/openshift/ptp-operator/test/pkg/pods"

	k8sv1 "k8s.io/api/core/v1"
)

const openshiftPtpNamespace = "openshift-ptp"
const openshiftPtpMetricPrefix = "openshift_ptp_"

// Needed to deserialize prometheus query output.
// Sample output (omiting irrelevant fields):
//
//	{"data" : {
//	   "result" : [{
//	     "metric" : {
//	       "pod" : "mm-pod1",
//	}}]}}
type queryOutput struct {
	Data data
}

type data struct {
	Result []result
}

type result struct {
	Metric metric
}

type metric struct {
	Pod string
}

func collectPrometheusMetrics(uniqueMetricKeys []string) map[string][]string {
	prometheusPod, err := metrics.GetPrometheusPod()
	Expect(err).ToNot(HaveOccurred(), "failed to get prometheus pod")

	podsPerPrometheusMetricKey := make(map[string][]string)
	var failedQueries []string // Collect failed queries for debugging

	for _, metricsKey := range uniqueMetricKeys {
		var promResult []result
		promResponse := metrics.PrometheusQueryResponse{}
		promResponse.Data.Result = &promResult

		errs := metrics.RunPrometheusQuery(prometheusPod, metricsKey, &promResponse)
		if errs != nil {
			failedQueries = append(failedQueries, fmt.Sprintf("Query failed for metric: %s", metricsKey))
			continue // Skip this metric and collect others
		}
		// Handle potential marshaling errors
		if promResponse.Data.Result == nil {
			failedQueries = append(failedQueries, fmt.Sprintf("Empty or nil result for metric: %s", metricsKey))
			continue
		}

		var podsPerKey []string
		for _, results := range promResult {
			podsPerKey = append(podsPerKey, results.Metric.Pod)
		}
		podsPerPrometheusMetricKey[metricsKey] = podsPerKey
	}

	// Debugging Output
	if len(failedQueries) > 0 {
		log.Printf("Some Prometheus queries failed (%d total):\n%s\n", len(failedQueries), strings.Join(failedQueries, "\n"))
		Expect(len(failedQueries)).To(Equal(0), "Some Prometheus queries failed")
	}

	// Debug Map Size
	log.Printf("Collected %d unique Prometheus metrics\n", len(podsPerPrometheusMetricKey))

	return podsPerPrometheusMetricKey
}

func collectPtpMetrics(ptpPods []k8sv1.Pod) (map[string][]string, []string) {
	uniqueMetricKeys := []string{}
	ptpMonitoredEntriesByPod := map[string][]string{}
	for _, pod := range ptpPods {
		podEntries := []string{}
		var stdout bytes.Buffer
		var err error
		Eventually(func() error {
			stdout, _, err = pods.ExecCommand(client.Client, &pod, pod.Spec.Containers[0].Name, []string{"curl", "localhost:9091/metrics"})
			if len(strings.Split(stdout.String(), "\n")) == 0 {
				return fmt.Errorf("empty response")
			}

			return err
		}, 2*time.Minute, 2*time.Second).Should(Not(HaveOccurred()))

		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.HasPrefix(line, openshiftPtpMetricPrefix) {
				metricsKey := line[0:strings.Index(line, "{")]
				podEntries = append(podEntries, metricsKey)
				uniqueMetricKeys = appendIfMissing(uniqueMetricKeys, metricsKey)
			}
		}
		ptpMonitoredEntriesByPod[pod.Name] = podEntries
	}
	return ptpMonitoredEntriesByPod, uniqueMetricKeys
}

func containSameMetrics(ptpMetricsByPod map[string][]string, prometheusMetrics map[string][]string) error {
	var missingMetrics []string
	maxErrors := 5 // Limit the number of reported missing metrics

	for podName, monitoringKeys := range ptpMetricsByPod {
		for _, key := range monitoringKeys {
			if podsWithMetric, ok := prometheusMetrics[key]; ok {
				if hasElement(podsWithMetric, podName) {
					continue
				}
			}
			// Collect missing metric details
			missingMetrics = append(missingMetrics, fmt.Sprintf("metric %s on pod %s", key, podName))
			// Stop collecting after maxErrors to prevent excessive output
			if len(missingMetrics) >= maxErrors {
				missingMetrics = append(missingMetrics, "...")
				return fmt.Errorf("some metrics were not reported:\n%s", strings.Join(missingMetrics, "\n"))
			}
		}
	}

	if len(missingMetrics) > 0 {
		return fmt.Errorf("some metrics were not reported:\n%s", strings.Join(missingMetrics, "\n"))
	}

	return nil
}

func hasElement(slice []string, item string) bool {
	for _, sliceItem := range slice {
		if item == sliceItem {
			return true
		}
	}
	return false
}

func appendIfMissing(slice []string, newItem string) []string {
	if hasElement(slice, newItem) {
		return slice
	}
	return append(slice, newItem)
}
