package controller

import (
	"github.com/openshift/ptp-operator/pkg/controller/ptpcfg"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, ptpcfg.Add)
}
