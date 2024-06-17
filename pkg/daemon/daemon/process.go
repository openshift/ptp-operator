package daemon

import "github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/config"

type process interface {
	Name() string
	Stopped() bool
	CmdStop()
	CmdInit()
	CmdRun(stdToSocket bool)
	MonitorProcess(p config.ProcessConfig)
	ExitCh() chan struct{}
}
