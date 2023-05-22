package daemon

import "github.com/openshift/linuxptp-daemon/pkg/event"

type process interface {
	Name() string
	Stopped() bool
	cmdStop()
	cmdInit()
	cmdRun(stdToSocket bool)
	monitorEvent(clockType event.ClockType, configName string, chClose chan bool, chEventChannel chan<- event.EventChannel)
}
