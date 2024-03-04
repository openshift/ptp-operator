package daemon_test

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/openshift/linuxptp-daemon/pkg/config"
	"github.com/openshift/linuxptp-daemon/pkg/daemon"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

const (
	s0     = 0.0
	s1     = 2.0
	s2     = 1.0
	MYNODE = "mynode"
	// SKIP skip the verification of the metric
	SKIP    = 12345678
	CLEANUP = -12345678
)

var pm *daemon.ProcessManager
var registry *prometheus.Registry

type TestCase struct {
	MessageTag                  string
	Name                        string
	Ifaces                      config.IFaces
	log                         string
	from                        string
	process                     string
	node                        string
	iface                       string
	expectedOffset              float64 // offset_ns
	expectedMaxOffset           float64 // max_offset_ns1
	expectedFrequencyAdjustment float64 // frequency_adjustment_ns
	expectedDelay               float64 // delay_ns
	expectedClockState          float64 // clock_state
	expectedNmeaStatus          float64 // nmea_status
	expectedPpsStatus           float64 // pps_status
	expectedClockClassMetrics   float64 // clock_class
	expectedInterfaceRole       float64 // role
}

func (tc *TestCase) init() {
	tc.expectedOffset = SKIP
	tc.expectedMaxOffset = SKIP
	tc.expectedFrequencyAdjustment = SKIP
	tc.expectedDelay = SKIP
	tc.expectedClockState = SKIP
	tc.expectedNmeaStatus = SKIP
	tc.expectedPpsStatus = SKIP
	tc.expectedClockClassMetrics = SKIP
	tc.expectedInterfaceRole = SKIP
}

func (tc *TestCase) String() string {
	b := strings.Builder{}
	b.WriteString("log: \"" + tc.log + "\"\n")
	b.WriteString("from: " + tc.from + "\n")
	b.WriteString("process: " + tc.process + "\n")
	b.WriteString("node: " + tc.node + "\n")
	b.WriteString("iface: " + tc.iface + "\n")
	return b.String()
}

func (tc *TestCase) cleanupMetrics() {
	daemon.Offset.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface}).Set(CLEANUP)
	daemon.MaxOffset.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface}).Set(CLEANUP)
	daemon.FrequencyAdjustment.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface}).Set(CLEANUP)
	daemon.Delay.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface}).Set(CLEANUP)
	daemon.ClockState.With(map[string]string{"process": tc.process, "node": tc.node, "iface": tc.iface}).Set(CLEANUP)
	daemon.ClockClassMetrics.With(map[string]string{"process": tc.process, "node": tc.node}).Set(CLEANUP)
	daemon.InterfaceRole.With(map[string]string{"process": tc.process, "node": tc.node, "iface": tc.iface}).Set(CLEANUP)
}

var testCases = []TestCase{
	{
		log:                         "phc2sys[1823126.732]: [ptp4l.0.config] CLOCK_REALTIME phc offset       -10 s2 freq   +8956 delay    508",
		MessageTag:                  "[ptp4l.0.config]",
		Name:                        "phc2sys",
		from:                        "phc",
		process:                     "phc2sys",
		iface:                       "CLOCK_REALTIME",
		expectedOffset:              -10,
		expectedMaxOffset:           -10,
		expectedFrequencyAdjustment: 8956,
		expectedDelay:               508,
		expectedClockState:          s2,
		expectedNmeaStatus:          SKIP,
		expectedPpsStatus:           SKIP,
		expectedClockClassMetrics:   SKIP,
		expectedInterfaceRole:       SKIP,
	},
	{
		log:                         "ts2phc[1896327.319]: [ts2phc.0.config] ens2f0 master offset         -1 s2 freq      -2",
		MessageTag:                  "[ts2phc.0.config]",
		Name:                        "ts2phc",
		from:                        "master",
		process:                     "ts2phc",
		iface:                       "ens2fx",
		expectedOffset:              -1,
		expectedMaxOffset:           -1,
		expectedFrequencyAdjustment: -2,
		expectedDelay:               0,
		expectedClockState:          s2,
		expectedNmeaStatus:          SKIP,
		expectedPpsStatus:           SKIP,
		expectedClockClassMetrics:   SKIP,
		expectedInterfaceRole:       SKIP,
	},
	{
		log:                         "ts2phc[1896327.319]: [ts2phc.0.config] ens2f0 master offset         3 s0 freq      4",
		MessageTag:                  "[ts2phc.0.config]",
		Name:                        "ts2phc",
		from:                        "master",
		process:                     "ts2phc",
		iface:                       "ens2fx",
		expectedOffset:              3,
		expectedMaxOffset:           3,
		expectedFrequencyAdjustment: 4,
		expectedDelay:               0,
		expectedClockState:          s0,
		expectedNmeaStatus:          SKIP,
		expectedPpsStatus:           SKIP,
		expectedClockClassMetrics:   SKIP,
		expectedInterfaceRole:       SKIP,
	},
	{
		log:                         "ptp4l[8542280.698]: [ptp4l.0.config] port 1: UNCALIBRATED to SLAVE on MASTER_CLOCK_SELECTED",
		MessageTag:                  "[ptp4l.0.config]",
		Name:                        "ptp4l",
		from:                        "master",
		process:                     "ptp4l",
		iface:                       "ens3f2",
		expectedOffset:              SKIP,
		expectedMaxOffset:           SKIP,
		expectedFrequencyAdjustment: SKIP,
		expectedDelay:               SKIP,
		expectedClockState:          SKIP,
		expectedNmeaStatus:          SKIP,
		expectedPpsStatus:           SKIP,
		expectedClockClassMetrics:   SKIP,
		expectedInterfaceRole:       1,
		Ifaces: []config.Iface{
			{
				Name:     "ens3f2",
				IsMaster: false,
				Source:   "",
				PhcId:    "phcid-2",
			},
		},
	},
	{
		log:                         "ptp4l[8537738.636]: [ptp4l.0.config] port 1: SLAVE to FAULTY on FAULT_DETECTED (FT_UNSPECIFIED)",
		MessageTag:                  "[ptp4l.0.config]",
		Name:                        "ptp4l",
		from:                        "master",
		process:                     "ptp4l",
		iface:                       "ens3fx",
		expectedOffset:              999999, // faultyOffset
		expectedMaxOffset:           999999, // faultyOffset
		expectedFrequencyAdjustment: 0,
		expectedDelay:               0,
		expectedClockState:          s0, // FREERUN
		expectedNmeaStatus:          SKIP,
		expectedPpsStatus:           SKIP,
		expectedClockClassMetrics:   SKIP,
		expectedInterfaceRole:       SKIP,
		Ifaces: []config.Iface{
			{
				Name:     "ens3f2",
				IsMaster: false,
				Source:   "",
				PhcId:    "phcid-2",
			},
		},
	},
}

func setup() {
	flag.Set("alsologtostderr", fmt.Sprintf("%t", true))
	var logLevel string
	flag.StringVar(&logLevel, "logLevel", "4", "test")
	flag.Lookup("v").Value.Set(logLevel)

	daemon.InitializeOffsetMaps()
	pm = daemon.NewProcessManager()
	daemon.RegisterMetrics(MYNODE)
}

func teardown() {
}

func TestMain(m *testing.M) {
	setup()
	code := m.Run()
	teardown()
	os.Exit(code)
}
func Test_ProcessPTPMetrics(t *testing.T) {

	assert := assert.New(t)
	for _, tc := range testCases {
		tc.node = MYNODE
		tc.cleanupMetrics()
		pm.SetTestData(tc.Name, tc.MessageTag, tc.Ifaces)
		pm.RunProcessPTPMetrics(tc.log)

		if tc.expectedOffset != SKIP {
			ptpOffset := daemon.Offset.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface})
			assert.Equal(tc.expectedOffset, testutil.ToFloat64(ptpOffset), "Offset does not match\n%s", tc.String())
		}
		if tc.expectedMaxOffset != SKIP {
			ptpMaxOffset := daemon.MaxOffset.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface})
			assert.Equal(tc.expectedMaxOffset, testutil.ToFloat64(ptpMaxOffset), "MaxOffset does not match\n%s", tc.String())
		}
		if tc.expectedFrequencyAdjustment != SKIP {
			ptpFrequencyAdjustment := daemon.FrequencyAdjustment.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface})
			assert.Equal(tc.expectedFrequencyAdjustment, testutil.ToFloat64(ptpFrequencyAdjustment), "FrequencyAdjustment does not match\n%s", tc.String())
		}
		if tc.expectedDelay != SKIP {
			ptpDelay := daemon.Delay.With(map[string]string{"from": tc.from, "process": tc.process, "node": tc.node, "iface": tc.iface})
			assert.Equal(tc.expectedDelay, testutil.ToFloat64(ptpDelay), "Delay does not match\n%s", tc.String())
		}
		if tc.expectedClockState != SKIP {
			clockState := daemon.ClockState.With(map[string]string{"process": tc.process, "node": tc.node, "iface": tc.iface})
			assert.Equal(tc.expectedClockState, testutil.ToFloat64(clockState), "ClockState does not match\n%s", tc.String())
		}
		if tc.expectedClockClassMetrics != SKIP {
			clockClassMetrics := daemon.ClockClassMetrics.With(map[string]string{"process": tc.process, "node": tc.node})
			assert.Equal(tc.expectedClockClassMetrics, testutil.ToFloat64(clockClassMetrics), "ClockClassMetrics does not match\n%s", tc.String())
		}
		if tc.expectedInterfaceRole != SKIP {
			role := daemon.InterfaceRole.With(map[string]string{"process": tc.process, "node": tc.node, "iface": tc.iface})
			assert.Equal(tc.expectedInterfaceRole, testutil.ToFloat64(role), "InterfaceRole does not match\n%s", tc.String())
		}
	}
}
