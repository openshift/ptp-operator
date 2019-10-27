package controller

import (
	"github.com/openshift/ptp-operator/pkg/controller/ptpconfig"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, ptpconfig.Add)
}
