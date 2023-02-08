package mapping

import (
	"github.com/openshift/linuxptp-daemon/addons/generic"
	"github.com/openshift/linuxptp-daemon/addons/intel"
	"github.com/openshift/linuxptp-daemon/pkg/plugin"
)

var PluginMapping = map[string]plugin.New{
	"reference": generic.Reference,
	"e810":      intel.E810,
}
