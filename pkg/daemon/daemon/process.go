package daemon

import "github.com/openshift/linuxptp-daemon/pkg/config"

type process interface {
	Name() string
	Stopped() bool
	CmdStop()
	CmdInit()
	CmdRun(stdToSocket bool)
	MonitorProcess(p config.ProcessConfig)
	ExitCh() chan struct{}
}
