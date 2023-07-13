package dpll_test

import (
	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon/dpll"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/stretchr/testify/assert"
	"strconv"
	"strings"
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

func TestSysfs(t *testing.T) {
	//indexStr := fmt.Sprintf("/sys/class/net/%s/ifindex", "lo")
	//fContent, err := os.ReadFile(indexStr)
	//assert.Nil(t, err)
	fcontentStr := strings.ReplaceAll("-26644444444444444", "\n", "")
	index, err2 := strconv.ParseInt(fcontentStr, 10, 64)
	glog.Errorf("errr %s", err2)
	assert.Nil(t, err2)
	assert.GreaterOrEqual(t, index, int64(-26644444444444444))
}
