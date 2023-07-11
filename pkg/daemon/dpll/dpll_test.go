package dpll_test

import (
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon/dpll"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestDpllConfig_MonitorProcess(t *testing.T) {
	d := dpll.NewDpll(1400, 5, 10, "ens01", []event.EventSource{})
	eventChannel := make(chan event.EventChannel, 10)

	d.MonitorProcess(config.ProcessConfig{
		ClockType:       "GM",
		ConfigName:      "test",
		EventChannel:    eventChannel,
		GMThreshold:     config.Threshold{},
		InitialPTPState: event.PTP_FREERUN,
	})

	ptpState := <-eventChannel
	assert.Equal(t, ptpState.ProcessName, event.DPLL)
}
