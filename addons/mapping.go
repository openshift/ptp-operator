package mapping

import (
	"github.com/k8snetworkplumbingwg/ptp-operator/addons/generic"
	"github.com/k8snetworkplumbingwg/ptp-operator/addons/intel"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/plugin"
)

var PluginMapping = map[string]plugin.New{
	"reference": generic.Reference,
	"e810":      intel.E810,
}
