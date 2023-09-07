package dpll_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon/dpll"
	"github.com/openshift/linuxptp-daemon/pkg/event"
	"github.com/stretchr/testify/assert"
)

func TestDpllConfig_MonitorProcess(t *testing.T) {
	eChannel := make(chan event.EventChannel, 10)
	closeChn := make(chan bool)
	// event has to be running before dpll is started
	event.Init("node", false, "/tmp/go.sock", eChannel, closeChn, nil, nil)
	d := dpll.NewDpll(248, 1400, 5, 10, "ens01", []event.EventSource{})
	eventChannel := make(chan event.EventChannel, 10)
	if d != nil {
		d.MonitorProcess(config.ProcessConfig{
			ClockType:       "GM",
			ConfigName:      "test",
			EventChannel:    eventChannel,
			GMThreshold:     config.Threshold{},
			InitialPTPState: event.PTP_FREERUN,
		})

		select {
		case ptpState := <-eventChannel:
			assert.Equal(t, ptpState.ProcessName, event.DPLL)
		case <-time.After(time.Millisecond * 250):
			glog.Error("Failed to send DPLL event")
		}

	}

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

type dpllTestCase struct {
	localMaxHoldoverOffSet uint64
	localHoldoverTimeout   uint64
	maxInSpecOffset        uint64
	expectedSlope          float64
	expectedTimeout        int64
}

func TestSlopeAndTimer(t *testing.T) {

	testCase := []dpllTestCase{
		{
			localMaxHoldoverOffSet: 2000,
			localHoldoverTimeout:   100,
			maxInSpecOffset:        100,
			expectedSlope:          20000,
			expectedTimeout:        5,
		},
	}
	for _, tt := range testCase {
		d := dpll.NewDpll(100, tt.localMaxHoldoverOffSet, tt.localHoldoverTimeout, tt.maxInSpecOffset,
			"test", []event.EventSource{})
		assert.Equal(t, tt.localMaxHoldoverOffSet, d.LocalMaxHoldoverOffSet, "localMaxHoldover offset")
		assert.Equal(t, tt.localHoldoverTimeout, d.LocalHoldoverTimeout, "Local holdover timeout")
		assert.Equal(t, tt.maxInSpecOffset, d.MaxInSpecOffset, "Max In Spec Offset")
		assert.Equal(t, tt.expectedTimeout, d.Timer(), "Timer in secs")
		assert.Equal(t, tt.expectedSlope, d.Slope(), "Slope")
	}
}
