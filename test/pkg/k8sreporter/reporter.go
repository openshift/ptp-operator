// SPDX-License-Identifier:Apache-2.0

package k8sreporter

import (
	"os"
	"strings"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"github.com/openshift/ptp-operator/test/pkg"

	"github.com/openshift-kni/k8sreporter"
	"k8s.io/apimachinery/pkg/runtime"
)

func New(reportPath string) (*k8sreporter.KubernetesReporter, error) {
	addToScheme := func(s *runtime.Scheme) error {
		err := ptpv1.AddToScheme(s)
		if err != nil {
			return err
		}
		return nil
	}

	dumpNamespace := func(ns string) bool {
		switch {
		case ns == pkg.PtpNamespace:
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

	err := os.Mkdir(reportPath, 0755)
	if err != nil {
		return nil, err
	}

	reporter, err := k8sreporter.New("", addToScheme, dumpNamespace, reportPath, crds...)
	if err != nil {
		return nil, err
	}
	return reporter, nil
}
