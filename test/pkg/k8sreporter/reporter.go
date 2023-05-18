// SPDX-License-Identifier:Apache-2.0

package k8sreporter

import (
	"log"
	"strings"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"

	"github.com/openshift-kni/k8sreporter"
	"k8s.io/apimachinery/pkg/runtime"
)

func New(kubeconfig, path, namespace string) *k8sreporter.KubernetesReporter {
	addToScheme := func(s *runtime.Scheme) error {
		err := ptpv1.AddToScheme(s)
		if err != nil {
			return err
		}
		return nil
	}

	dumpNamespace := func(ns string) bool {
		switch {
		case ns == namespace:
			return true
		case strings.HasPrefix(ns, "ptp"):
			return true
		}
		return false
	}

	crds := []k8sreporter.CRData{
		{Cr: &ptpv1.NodePtpDeviceList{}},
		{Cr: &ptpv1.PtpConfigList{}},
		{Cr: &ptpv1.PtpOperatorConfigList{}},
	}

	reporter, err := k8sreporter.New(kubeconfig, addToScheme, dumpNamespace, path, crds...)
	if err != nil {
		log.Fatalf("Failed to initialize the reporter %s", err)
	}
	return reporter
}
