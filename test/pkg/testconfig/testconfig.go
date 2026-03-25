package testconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptptestconfig "github.com/k8snetworkplumbingwg/ptp-operator/test/conformance/config"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/clean"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/k8sutil"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/metrics"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/nodes"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/ptphelper"
	l2lib "github.com/redhat-cne/l2discovery-lib"
	solver "github.com/redhat-cne/l2discovery-lib/pkg/graphsolver"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	v1core "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (

	// DualNICBoundaryClockString matches the Discovery clock mode in Environement
	DiscoveryString = "Discovery"
	// legacyDiscoveryString matches the legacy Discovery clock mode in Environement
	legacyDiscoveryString = "true"
	// NoneString matches empty environment variable
	NoneString = ""
	// StartString Stringer value for the start status
	StartString = "Start"
	// InitStatusString Stringer value for the init status
	InitStatusString = "init"
	// ConfiguredStatusString Stringer value for the configured status
	ConfiguredStatusString = "configured"
	// DiscoverySuccessStatusString Stringer value for the Discovery Success status
	DiscoverySuccessStatusString = "discoverySuccess"
	// DiscoveryFailureStatusString Stringer value for the Discovery failure status
	DiscoveryFailureStatusString = "discoveryFailure"
	PtpLinuxDaemonNamespace      = "openshift-ptp"
	defaultSchedulingPriority    = 65
	defaultPriority              = 5
	// OrdinaryClockString matches the OC clock mode in Environement
	OrdinaryClockString = "OC"
	// DualFollowerClocktring matches the DualFollower clock mode in Environement
	DualFollowerClockString = "DualFollower"
	// BoundaryClockString matches the BC clock mode in Environement
	BoundaryClockString = "BC"
	// DualNICBoundaryClockString matches the DualNICBC clock mode in Environement
	DualNICBoundaryClockString = "DualNICBC"
	// DualNICBoundaryClockHAString matches the DualNICBC HA clock mode in Environment
	DualNICBoundaryClockHAString = "DualNICBCHA"
	// TelcoGrandMasterClockString matches the T-GM clock mode in Environement
	TelcoGrandMasterClockString = "TGM"
	// TelcoBoundaryClockString matches the T-BC clock mode in Environement
	TelcoBoundaryClockString = "TBC"
	ptp4lEthernet            = "-2 --summary_interval -4"
	ptp4lEthernetSlave       = "-2 -s --summary_interval -4"
	phc2sysGM                = "-a -r -r -n 24" // use phc2sys to sync phc to system clock
	phc2sysSlave             = "-a -r -n 24 -m -N 8 -R 16"
	phc2sysDualNicBCHA       = "-a -r -m -l 7 -n 24 "
	SCHED_OTHER              = "SCHED_OTHER"
	SCHED_FIFO               = "SCHED_FIFO"
	L2_DISCOVERY_IMAGE       = "quay.io/redhat-cne/l2discovery:v17"
)

// Mirrors graphsolver step constants.
// Kept local to avoid vendor/replace mismatches during tests.
const (
	solverStepClockClassLessThanInDomain = 0
	solverOneIfaceTwoValues              = 11
)

// stepClockClassLessThanInDomain creates a combined step that checks both clock class < threshold
// AND domain number on the SAME announce message. This prevents false matches where clock class
// passes on one GM's announce but domain passes on a different GM's announce.
func stepClockClassLessThanInDomain(ifParam, clockClassThreshold, domain int) []int {
	return []int{solverStepClockClassLessThanInDomain, solverOneIfaceTwoValues, ifParam, clockClassThreshold, domain, solver.Positive}
}

func getL2DiscoveryImage() string {
	if img := os.Getenv("L2_DISCOVERY_IMAGE"); img != "" {
		return img
	}
	return L2_DISCOVERY_IMAGE
}

type ConfigStatus int64

const (
	// Start starting status when object is created
	Start ConfigStatus = iota
	// InitStatus the configuration environment variable was read
	InitStatus
	// ConfiguredStatus for OC/BC/DuallinkBC modes this is set after the ptp clock is configured
	ConfiguredStatus
	// DiscoverySuccessStatus for all modes, indicates a successful discovery
	DiscoverySuccessStatus
	// DiscoveryFailureStatus for all modes, indicates a discovery failure
	DiscoveryFailureStatus
)

type PTPMode int64

const (
	// OrdinaryClock OrdinaryClock mode
	OrdinaryClock PTPMode = iota
	// BoundaryClock Boundary Clock mode
	BoundaryClock
	// DualNICBoundaryClock DualNIC Boundary Clock mode
	DualNICBoundaryClock
	// DualNICBoundaryClockHA DualNIC Boundary Clock HA mode
	DualNICBoundaryClockHA
	// GrandMaster mode
	TelcoGrandMasterClock
	// TelcoBoundary Clock mode
	TelcoBoundaryClock
	// Discovery Discovery mode
	Discovery
	// None initial empty mode
	None
	//Dual Follower mode
	DualFollowerClock
)

type TestConfig struct {
	PtpModeDesired    PTPMode
	PtpModeDiscovered PTPMode
	Status            ConfigStatus
	DiscoveredGrandMasterPtpConfig,
	DiscoveredSlave1PtpConfig,
	DiscoveredSlave2PtpConfig,
	DiscoveredClockUnderTestPtpConfig,
	DiscoveredClockUnderTestSecondaryPtpConfig *ptpDiscoveryRes
	DiscoveredClockUnderTestPod  *v1core.Pod
	DiscoveredMasterInterfaces   []string
	DiscoveredFollowerInterfaces []string
	L2Config                     l2lib.L2Info
	FoundSolutions               map[string]bool
	PtpEventsIsConsumerReady     bool
}
type solverData struct {
	// Mapping between clock role and port depending on the algo
	testClockRolesAlgoMapping map[string]*[]int
	// map storing solutions
	solutions map[string]*[][]int

	problems map[string]*[][][]int
}

var enabledProblems = []string{AlgoOCString,
	AlgoBCString,
	AlgoBCWithSlavesString,
	AlgoDualNicBCString,
	AlgoDualNicBCWithSlavesString,
	AlgoTelcoGMString,
	AlgoTelcoBCString,
	AlgoTelcoBCWithSlavesString,
	AlgoTelcoBCExtGMString,
	AlgoTelcoBCWithSlavesExtGMString,
	AlgoOCExtGMString,
	AlgoBCExtGMString,
	AlgoBCWithSlavesExtGMString,
	AlgoDualNicBCExtGMString,
	AlgoDualNicBCWithSlavesExtGMString,
	AlgoDualFollowerString,
	AlgoDualFollowerExtGMString,
}

const FirstSolution = 0

var data solverData

// indicates the clock roles in the algotithms
type TestIfClockRoles int

const NumTestClockRoles = 8
const (
	Grandmaster TestIfClockRoles = iota
	GrandmasterSibling
	Slave1
	Slave2
	BC1Master
	BC1Slave
	BC2Master
	BC2Slave
)

const (
	AlgoOCString                       = "OC"
	AlgoDualFollowerString             = "DualFollower"
	AlgoBCString                       = "BC"
	AlgoBCWithSlavesString             = "BCWithSlaves"
	AlgoDualNicBCString                = "DualNicBC"
	AlgoTelcoGMString                  = "TGM"
	AlgoTelcoBCString                  = "TBC"
	AlgoTelcoBCWithSlavesString        = "TBCWithSlaves"
	AlgoDualNicBCWithSlavesString      = "DualNicBCWithSlaves"
	AlgoTelcoBCExtGMString             = "TBCExtGM"
	AlgoTelcoBCWithSlavesExtGMString   = "TBCWithSlavesExtGM"
	AlgoOCExtGMString                  = "OCExtGM"
	AlgoDualFollowerExtGMString        = "DualFollowerExtGM"
	AlgoBCExtGMString                  = "BCExtGM"
	AlgoDualNicBCExtGMString           = "DualNicBCExtGM"
	AlgoBCWithSlavesExtGMString        = "BCWithSlavesExtGM"
	AlgoDualNicBCWithSlavesExtGMString = "DualNicBCWithSlavesExtGM"
)

type ptpDiscoveryRes ptpv1.PtpConfig

// BasePtp4lConfig is the base ptp4l configuration template
const BasePtp4lConfig = `[global]
#
# Default Data Set
#
twoStepFlag 1
domainNumber 24
#utc_offset 37
clockAccuracy 0xFE
offsetScaledLogVariance 0xFFFF
free_running 0
freq_est_interval 1
dscp_event 0
dscp_general 0
dataset_comparison G.8275.x
G.8275.defaultDS.localPriority 128
#
# Port Data Set
#
logAnnounceInterval -3
logSyncInterval -4
logMinDelayReqInterval -4
logMinPdelayReqInterval -4
announceReceiptTimeout 6
syncReceiptTimeout 0
delayAsymmetry 0
fault_reset_interval -4
neighborPropDelayThresh 20000000
G.8275.portDS.localPriority 128
#
# Run time options
#
assume_two_step 0
logging_level 6
path_trace_enabled 0
follow_up_info 0
hybrid_e2e 0
inhibit_multicast_service 0
net_sync_monitor 0
tc_spanning_tree 0
tx_timestamp_timeout 50
unicast_listen 0
unicast_master_table 0
unicast_req_duration 3600
use_syslog 1
verbose 1
summary_interval -4
kernel_leap 1
check_fup_sync 0
clock_class_threshold 7
#
# Servo Options
#
pi_proportional_const 0.0
pi_integral_const 0.0
pi_proportional_scale 0.0
pi_proportional_exponent -0.3
pi_proportional_norm_max 0.7
pi_integral_scale 0.0
pi_integral_exponent 0.4
pi_integral_norm_max 0.3
step_threshold 2.0
first_step_threshold 0.00002
max_frequency 900000000
clock_servo pi
sanity_freq_limit 200000000
ntpshm_segment 0
#
# Transport options
#
transportSpecific 0x0
ptp_dst_mac 01:1B:19:00:00:00
p2p_dst_mac 01:80:C2:00:00:0E
udp_ttl 1
udp6_scope 0x0E
uds_address /var/run/ptp4l
#
# Default interface options
#
network_transport L2
delay_mechanism E2E
time_stamping hardware
tsproc_mode filter
delay_filter moving_median
delay_filter_length 10
egressLatency 0
ingressLatency 0
#
# Clock description
#
productDescription ;;
revisionData ;;
manufacturerIdentity 00:00:00
userDescription ;
timeSource 0xA0
`

// GetPtp4lConfigWithAuth returns the base config with optional auth settings
// When auth is enabled:
// - [global] gets sa_file + spp -1 (auth disabled by default, including UDS sockets)
// - Per-interface sections should get spp + active_key_id via AddInterfaceWithAuth()
func GetPtp4lConfigWithAuth(baseConfig string) string {
	authEnabled := os.Getenv("PTP_AUTH_ENABLED")
	if authEnabled == "true" {
		// Add auth settings to [global] section
		// spp -1 disables auth by default (allows phc2sys/pmc to use UDS without auth)
		// Per-interface auth is added via AddInterfaceWithAuth()
		authSettings := `sa_file /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf
spp -1
`
		// Insert auth settings after the [global] line
		lines := strings.Split(baseConfig, "\n")
		result := []string{}
		for i, line := range lines {
			result = append(result, line)
			// After [global] line and comments, insert auth
			if line == "[global]" && i+3 < len(lines) {
				// Skip comment lines, insert before twoStepFlag
				result = append(result, authSettings)
			}
		}
		return strings.Join(result, "\n")
	}
	return baseConfig
}

// IsAuthEnabled returns true if PTP_AUTH_ENABLED environment variable is set to "true"
func IsAuthEnabled() bool {
	return os.Getenv("PTP_AUTH_ENABLED") == "true"
}

const BaseTs2PhcConfig = `[nmea]
ts2phc.master 1
[global]
use_syslog  0
verbose 1
logging_level 6
ts2phc.pulsewidth 100000000
leapfile  /usr/share/zoneinfo/leap-seconds.list
`

func (obj *ptpDiscoveryRes) String() string {
	if obj == nil {
		return "nil"
	}
	return obj.Name
}
func (obj *TestConfig) String() (out string) {
	if obj == nil {
		return "nil"
	}
	out += fmt.Sprintf("PtpModeDesired= %s, PtpModeDiscovered= %s, Status= %s, DiscoveredClockUnderTestPtpConfig= %s, DiscoveredClockUnderTestSecondaryPtpConfig= %s, DiscoveredGrandMasterPtpConfig= %s, DiscoveredSlave1PtpConfig= %s, DiscoveredSlave2PtpConfig= %s, PtpEventsIsConsumerReady= %t, DiscoveredFollowerInterfaces=%v, DiscoveredMasterInterfaces=%v",
		obj.PtpModeDesired,
		obj.PtpModeDiscovered,
		obj.Status,
		obj.DiscoveredClockUnderTestPtpConfig,
		obj.DiscoveredClockUnderTestSecondaryPtpConfig,
		obj.DiscoveredGrandMasterPtpConfig,
		obj.DiscoveredSlave1PtpConfig,
		obj.DiscoveredSlave2PtpConfig,
		obj.PtpEventsIsConsumerReady,
		obj.DiscoveredFollowerInterfaces,
		obj.DiscoveredMasterInterfaces)
	if obj.DiscoveredClockUnderTestPod != nil {
		out += fmt.Sprintf("DiscoveredClockUnderTestPodName=%s, DiscoveredClockUnderTestNodeName=%s",
			obj.DiscoveredClockUnderTestPod.Name,
			obj.DiscoveredClockUnderTestPod.Spec.NodeName)
	}

	return out
}

func (status ConfigStatus) String() string {
	switch status {
	case Start:
		return StartString
	case InitStatus:
		return InitStatusString
	case ConfiguredStatus:
		return ConfiguredStatusString
	case DiscoverySuccessStatus:
		return DiscoverySuccessStatusString
	case DiscoveryFailureStatus:
		return DiscoveryFailureStatusString
	default:
		return StartString
	}
}

func (mode PTPMode) String() string {
	switch mode {
	case OrdinaryClock:
		return OrdinaryClockString
	case DualFollowerClock:
		return DualFollowerClockString
	case BoundaryClock:
		return BoundaryClockString
	case DualNICBoundaryClock:
		return DualNICBoundaryClockString
	case DualNICBoundaryClockHA:
		return DualNICBoundaryClockHAString
	case TelcoGrandMasterClock:
		return TelcoGrandMasterClockString
	case TelcoBoundaryClock:
		return TelcoBoundaryClockString
	case Discovery:
		return DiscoveryString
	case None:
		return NoneString
	default:
		return OrdinaryClockString
	}
}

func StringToMode(aString string) PTPMode {
	switch strings.ToLower(aString) {
	case strings.ToLower(OrdinaryClockString):
		return OrdinaryClock
	case strings.ToLower(DualFollowerClockString):
		return DualFollowerClock
	case strings.ToLower(BoundaryClockString):
		return BoundaryClock
	case strings.ToLower(DualNICBoundaryClockString):
		return DualNICBoundaryClock
	case strings.ToLower(DualNICBoundaryClockHAString):
		return DualNICBoundaryClockHA
	case strings.ToLower(TelcoGrandMasterClockString):
		return TelcoGrandMasterClock
	case strings.ToLower(TelcoBoundaryClockString):
		return TelcoBoundaryClock
	case strings.ToLower(DiscoveryString), strings.ToLower(legacyDiscoveryString):
		return Discovery
	case strings.ToLower(NoneString):
		return OrdinaryClock
	default:
		return OrdinaryClock
	}
}

var GlobalConfig TestConfig

func init() {
	Reset()
}

// resets the test configuration
func Reset() {
	GlobalConfig.PtpModeDesired = None
	GlobalConfig.PtpModeDiscovered = None
	GlobalConfig.Status = Start
}
func initFoundSolutions() {
	GlobalConfig.FoundSolutions = make(map[string]bool)
	for _, name := range enabledProblems {
		if len(*data.solutions[name]) > 0 {
			GlobalConfig.FoundSolutions[name] = true
		}
	}
}

// Gets te desired configuration from the environment
func GetDesiredConfig(forceUpdate bool) TestConfig {
	defer logrus.Infof("Current PTP test config=%s", &GlobalConfig)
	if GlobalConfig.Status == InitStatus && !forceUpdate {
		return GlobalConfig
	}
	legacyDiscoveryModeString := os.Getenv("DISCOVERY_MODE")
	modeString := os.Getenv("PTP_TEST_MODE")

	mode := StringToMode(legacyDiscoveryModeString)

	if mode != Discovery {
		mode = StringToMode(modeString)
	}

	switch mode {
	case OrdinaryClock, BoundaryClock, DualNICBoundaryClock, DualNICBoundaryClockHA, TelcoGrandMasterClock, DualFollowerClock, Discovery, TelcoBoundaryClock:
		logrus.Infof("%s mode detected", mode)
		GlobalConfig.PtpModeDesired = mode
		GlobalConfig.Status = InitStatus
		return GlobalConfig
	case None:
		logrus.Infof("No test mode specified using, %s mode. Specify the env variable PTP_TEST_MODE with one of %s, %s, %s, %s, %s, %s, %s, %s", OrdinaryClock, Discovery, OrdinaryClock, BoundaryClock, DualFollowerClockString, TelcoGrandMasterClock, DualNICBoundaryClockString, DualNICBoundaryClockHAString, TelcoBoundaryClockString)
		GlobalConfig.PtpModeDesired = OrdinaryClock
		GlobalConfig.Status = InitStatus
		return GlobalConfig
	default:
		logrus.Infof("%s is not a supported mode, assuming %s", mode, OrdinaryClock)
		GlobalConfig.PtpModeDesired = OrdinaryClock
		GlobalConfig.Status = InitStatus
		return GlobalConfig
	}
}

// createPtpConfigurations sets up PTP configs using the given context for cancellation-aware waits.
func createPtpConfigurations(ctx context.Context) error {
	// Initialize environment variables early so they're available for PtpConfig creation
	err := metrics.InitEnvIntParamConfig("MAX_OFFSET_IN_NS", metrics.MaxOffsetDefaultNs, &metrics.MaxOffsetNs)
	if err != nil {
		logrus.Errorf("Error initializing MAX_OFFSET_IN_NS: %v", err)
		return err
	}
	err = metrics.InitEnvIntParamConfig("MIN_OFFSET_IN_NS", metrics.MinOffsetDefaultNs, &metrics.MinOffsetNs)
	if err != nil {
		logrus.Errorf("Error initializing MIN_OFFSET_IN_NS: %v", err)
		return err
	}
	err = metrics.InitEnvIntParamConfig("MAX_IN_SPEC_OFFSET_NS", metrics.MaxInSpecOffsetDefaultNs, &metrics.MaxInSpecOffsetNs)
	if err != nil {
		logrus.Errorf("Error initializing MAX_IN_SPEC_OFFSET_NS: %v", err)
		return err
	}

	if GlobalConfig.PtpModeDesired != Discovery {
		// for external grand master, clean previous configuration so that it is not detected as a external grandmaster
		err := clean.All()
		if err != nil {
			logrus.Errorf("Error deleting labels and configuration, err=%s", err)
		}
		ptphelper.RestartPTPDaemon()
		ptphelper.WaitForPtpDaemonToExist()
	}
	// Initialize desired ptp config for all configs
	GetDesiredConfig(true)
	// in multi node configuration create ptp configs

	// Initialize l2 library
	l2lib.GlobalL2DiscoveryConfig.SetL2Client(client.Client, client.Client.Config)

	// if USE_CONTAINER_CMDS environment variable is present, use container commands (lspci, ethtool, ...)
	_, useContainerCmds := os.LookupEnv("USE_CONTAINER_CMDS")

	// Wait for stuck Terminating namespace before L2 init (vendor privileged-daemonset only waits 2m).
	if err := k8sutil.PreWaitPrivilegedDSNamespaceIfTerminating(
		ctx, pkg.L2DiscoveryNamespace, k8sutil.PrivilegedDaemonsetNamespaceStuckDeleteWait,
	); err != nil {
		return fmt.Errorf("waiting for %s namespace: %w", pkg.L2DiscoveryNamespace, err)
	}

	// Collect L2 info
	config, err := l2lib.GlobalL2DiscoveryConfig.GetL2DiscoveryConfig(true, false, useContainerCmds, getL2DiscoveryImage())
	if err != nil {
		return fmt.Errorf("getting L2 discovery info failed with err=%s", err)
	}
	logrus.Tracef("L2DiscoveryConfig: %s\n", config)
	logrus.Tracef("L2 ifListFiltered=%+v, ifListUnfiltered=%+v", config.GetPtpIfList(), config.GetPtpIfListUnfiltered())
	GlobalConfig.L2Config = config

	if GlobalConfig.PtpModeDesired != Discovery {
		// initialize L2 config in solver
		solver.GlobalConfig.SetL2Config(config)
		logrus.Infof("Ports getting PTP frames=%+v", config.GetPortsGettingPTP())
		for _, ptpIf := range config.GetPortsGettingPTP() {
			if len(ptpIf.Announces) == 0 {
				logrus.Infof("PTP announce missing iface=%s node=%s", ptpIf.IfName, ptpIf.NodeName)
				continue
			}
			for gmID, announce := range ptpIf.Announces {
				logrus.Infof("PTP announce iface=%s node=%s domain=%d clockClass=%d gmId=%s priority1=%d priority2=%d stepsRemoved=%d timeSource=%d",
					ptpIf.IfName, ptpIf.NodeName, announce.DomainNumber, announce.ClockClass,
					gmID, announce.GrandmasterPriority1, announce.GrandmasterPriority2,
					announce.StepsRemoved, announce.TimeSource)
			}
		}
		initAndSolveProblems()

		if len(data.solutions) == 0 {
			return fmt.Errorf("could not find a solution")
		}
		isExternalMaster := ptphelper.IsExternalGM()
		switch GlobalConfig.PtpModeDesired {
		case Discovery, None:
			logrus.Errorf("error creating ptpconfig Discovery, None not supported")
		case OrdinaryClock:
			return PtpConfigOC(isExternalMaster)
		case DualFollowerClock:
			return PtpConfigDualFollower(isExternalMaster)
		case BoundaryClock:
			return PtpConfigBC(isExternalMaster)
		case DualNICBoundaryClock:
			return PtpConfigDualNicBC(isExternalMaster, false)
		case DualNICBoundaryClockHA:
			return PtpConfigDualNicBC(isExternalMaster, true)
		case TelcoGrandMasterClock:
			isExternalMaster = false // WPC GM is the only GM under test
			return PtpConfigTelcoGM(isExternalMaster)
		case TelcoBoundaryClock:
			return PtpConfigTelcoBC(isExternalMaster)
		}
	}
	return nil
}

const retryDelay = 45 * time.Second

// CreatePtpConfigurationsWithRetry runs createPtpConfigurations up to maxAttempts when the error
// is likely transient (namespace stuck in Terminating during privileged-daemonset / L2 init).
func CreatePtpConfigurationsWithRetry(maxAttempts int) error {
	return CreatePtpConfigurationsWithRetryContext(context.Background(), maxAttempts)
}

// CreatePtpConfigurationsWithRetryContext is like CreatePtpConfigurationsWithRetry but allows
// the caller to pass a context for cancellation-aware retry delays.
func CreatePtpConfigurationsWithRetryContext(ctx context.Context, maxAttempts int) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var last error
	for i := 0; i < maxAttempts; i++ {
		last = createPtpConfigurations(ctx)
		if last == nil {
			return nil
		}
		if i < maxAttempts-1 && k8sutil.IsTransientL2OrPrivilegedNamespaceError(last) {
			logrus.Warnf("CreatePtpConfigurations attempt %d/%d failed (transient): %v; retrying after %v", i+1, maxAttempts, last, retryDelay)
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				timer.Stop()
			}
			continue
		}
		break
	}
	return last
}

func initAndSolveProblems() {

	// create maps
	data.problems = make(map[string]*[][][]int)
	data.solutions = make(map[string]*[][]int)
	data.testClockRolesAlgoMapping = make(map[string]*[]int)

	// initialize problems
	// The number of step must be equal to the number of interfaces (e.g. p0, p1, ...)
	// TODO: add the number of interface separately in the solver to dimension the results slice independently from
	// the number of constraints.
	data.problems[AlgoOCString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameLan2), 2, 0, 1}}, // step2
	}

	data.problems[AlgoDualFollowerString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameLan2), 2, 0, 1}}, // step2
		{{int(solver.StepSameLan2), 2, 1, 2}, // step3
			{int(solver.StepSameNic), 2, 0, 2}}, // step3

	}

	data.problems[AlgoBCString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameNic), 2, 0, 1}},  // step2
		{{int(solver.StepSameLan2), 2, 1, 2}}, // step3

	}
	data.problems[AlgoBCWithSlavesString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameLan2), 2, 0, 1}}, // step2
		{{int(solver.StepSameNic), 2, 1, 2}},  // step3
		{{int(solver.StepSameLan2), 2, 2, 3}, // step4
			{int(solver.StepSameNic), 2, 0, 3, solver.Negative},
			{int(solver.StepSameLan2), 2, 0, 3, solver.Negative}}, // step4 - downstream slaves and grandmaster must be on different nics
	}
	data.problems[AlgoDualNicBCString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameNic), 2, 0, 1}},  // step2
		{{int(solver.StepSameLan2), 2, 1, 2}}, // step3
		{{int(solver.StepSameNode), 2, 1, 3}, // step4
			{int(solver.StepSameLan2), 2, 2, 3}}, // step4
		{{int(solver.StepSameNic), 2, 3, 4},
			{int(solver.StepSameNic), 2, 1, 3, solver.Negative}}, // step5
	}
	data.problems[AlgoTelcoGMString] = &[][][]int{
		{{int(solver.StepIsWPCNic), 1, 0}}, // step1: first iface is WPC
		{{int(solver.StepIsWPCNic), 1, 1}, // step2: second iface is WPC
			{int(solver.StepSameNic), 2, 0, 1}}, //        and on the same NIC
	}

	// T-BC with local GM: WPC NIC required, receiver, two transmitters on same NIC, local GM
	data.problems[AlgoTelcoBCString] = &[][][]int{
		{{int(solver.StepIsWPCNic), 1, 0}},    // step1: T-BC receiver must be on WPC NIC
		{{int(solver.StepSameNic), 2, 0, 1}},  // step3: transmitter 1 on same NIC as receiver
		{{int(solver.StepSameNic), 2, 0, 2}},  // step4: transmitter 2 on same NIC as receiver
		{{int(solver.StepSameLan2), 2, 0, 3}}, // step5: local grandmaster on same LAN as receiver
		{{int(solver.StepIsWPCNic), 1, 3}},    // step6: local grandmaster is a WPC NIC
	}

	// T-BC with external GM: WPC NIC required, PTP receiver, two transmitters on same NIC
	data.problems[AlgoTelcoBCExtGMString] = &[][][]int{
		{{int(solver.StepIsWPCNic), 1, 0}},           // step1: T-BC receiver must be on WPC NIC
		{{int(solver.StepIsPTP), 1, 0}},              // step2: receiver gets PTP from external GM
		{stepClockClassLessThanInDomain(0, 135, 24)}, // step3: receiver sees GM with clock class < 135 on domain 24
		{{int(solver.StepSameNic), 2, 0, 1}},         // step4: transmitter 1 on same NIC as receiver
		{{int(solver.StepSameNic), 2, 0, 2}},         // step5: transmitter 2 on same NIC as receiver
	}

	// T-BC with slaves and local GM: WPC NIC required, slave, receiver, two transmitters on same NIC, local GM
	data.problems[AlgoTelcoBCWithSlavesString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1: slave interface (can be anything)
		{{int(solver.StepSameLan2), 2, 0, 2}}, // step2: Slave on the same lan as transmitters
		{{int(solver.StepSameLan2), 2, 0, 3}}, // step3: Slave on the same lan as transmitters
		{{int(solver.StepIsWPCNic), 1, 1}},    // step4: T-BC receiver must be on WPC NIC
		{{int(solver.StepSameNic), 2, 1, 2}},  // step5: transmitter 1 on same NIC as receiver
		{{int(solver.StepSameNic), 2, 1, 3}},  // step6: transmitter 2 on same NIC as receiver
		{{int(solver.StepSameLan2), 2, 1, 4}}, // step7: local grandmaster on same LAN as receiver
	}

	// T-BC with slaves and external GM: WPC NIC required, slave, receiver, two transmitters on same NIC
	data.problems[AlgoTelcoBCWithSlavesExtGMString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},                // step1: slave interface (can be anything)
		{{int(solver.StepSameLan2), 2, 0, 2}},        // step2: Slave on the same lan as transmitters
		{{int(solver.StepSameLan2), 2, 0, 3}},        // step3: Slave on the same lan as transmitters
		{{int(solver.StepIsWPCNic), 1, 1}},           // step4: T-BC receiver must be on WPC NIC
		{{int(solver.StepIsPTP), 1, 1}},              // step5: receiver gets PTP from external GM
		{stepClockClassLessThanInDomain(1, 135, 24)}, // step6: receiver sees GM with clock class < 135 on domain 24
		{{int(solver.StepSameNic), 2, 1, 2}},         // step7: transmitter 1 on same NIC as receiver
		{{int(solver.StepSameNic), 2, 1, 3}},         // step8: transmitter 2 on same NIC as receiver
	}

	data.problems[AlgoDualNicBCWithSlavesString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameLan2), 2, 0, 1}}, // step2
		{{int(solver.StepSameNic), 2, 1, 2}},  // step3
		{{int(solver.StepSameLan2), 2, 2, 3}}, // step4
		{{int(solver.StepSameNode), 2, 2, 4}, // step5
			{int(solver.StepSameLan2), 2, 3, 4}}, // step5
		{{int(solver.StepSameNic), 2, 4, 5}}, // step6
		{{int(solver.StepSameLan2), 2, 5, 6}, // step7
			{int(solver.StepSameNic), 2, 0, 3, solver.Negative}, // downstream slaves and grandmaster must be on different nics
			{int(solver.StepSameNic), 2, 6, 3, solver.Negative}, // downstream slaves and grandmaster must be on different nics
			{int(solver.StepSameNic), 2, 2, 4, solver.Negative}, // dual nic BC uses 2 different NICs
			{int(solver.StepSameLan2), 2, 4, 6, solver.Negative},
			{int(solver.StepSameLan2), 2, 0, 6, solver.Negative}, // Downstream slaves use different lans to not share same clock
			{int(solver.StepSameNic), 2, 0, 6, solver.Negative}}, // OC slave ports must be on different nics

	}
	data.problems[AlgoOCExtGMString] = &[][][]int{
		{{int(solver.StepIsPTP), 1, 0}}, // step1
	}
	data.problems[AlgoDualFollowerExtGMString] = &[][][]int{
		{{int(solver.StepIsPTP), 1, 0}}, // step1
		{{int(solver.StepSameNic), 2, 0, 1}, // step1
			{int(solver.StepIsPTP), 1, 1}},
	}
	data.problems[AlgoBCExtGMString] = &[][][]int{
		{{int(solver.StepIsPTP), 1, 0}},      // step1
		{{int(solver.StepSameNic), 2, 0, 1}}, // step2
	}
	data.problems[AlgoBCWithSlavesExtGMString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameLan2), 2, 0, 1}}, // step2
		{{int(solver.StepSameNic), 2, 1, 2}, // step3
			{int(solver.StepIsPTP), 1, 2}},
	}
	data.problems[AlgoDualNicBCExtGMString] = &[][][]int{
		{{int(solver.StepIsPTP), 1, 0}},      // step1
		{{int(solver.StepSameNic), 2, 0, 1}}, // step2
		{{int(solver.StepIsPTP), 1, 2}, // step3
			{int(solver.StepSameNode), 2, 0, 2}}, // step3
		{{int(solver.StepSameNic), 2, 2, 3},
			{int(solver.StepSameNic), 2, 0, 2, solver.Negative}}, // step4
	}
	data.problems[AlgoDualNicBCWithSlavesExtGMString] = &[][][]int{
		{{int(solver.StepNil), 0, 0}},         // step1
		{{int(solver.StepSameLan2), 2, 0, 1}}, // step2
		{{int(solver.StepSameNic), 2, 1, 2}},  // step3
		{{int(solver.StepSameNode), 2, 2, 4}, // step4
			{int(solver.StepIsPTP), 1, 2},
			{int(solver.StepIsPTP), 1, 4}},
		{{int(solver.StepSameNic), 2, 4, 5}}, // step5
		{{int(solver.StepSameLan2), 2, 5, 6}, // step6
			{int(solver.StepSameNic), 2, 0, 3, solver.Negative},  // downstream slaves and grandmaster must be on different nics
			{int(solver.StepSameNic), 2, 6, 3, solver.Negative}}, // downstream slaves and grandmaster must be on different nics
		{{int(solver.StepSameNic), 2, 2, 4, solver.Negative}, // step 7 dual nic BC uses 2 different NICs
			{int(solver.StepSameNic), 2, 0, 6, solver.Negative}}, // OC slave ports must be on different nics
	}
	// Initializing Solution decoding and mapping
	// allocating all slices
	for _, name := range enabledProblems {
		alloc := make([]int, NumTestClockRoles)
		data.testClockRolesAlgoMapping[name] = &alloc
	}

	// OC
	(*data.testClockRolesAlgoMapping[AlgoOCString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoOCString])[Grandmaster] = 1

	// Dual Follower
	(*data.testClockRolesAlgoMapping[AlgoDualFollowerString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoDualFollowerString])[Grandmaster] = 1
	(*data.testClockRolesAlgoMapping[AlgoDualFollowerString])[Slave2] = 2

	// BC

	(*data.testClockRolesAlgoMapping[AlgoBCString])[BC1Slave] = 0
	(*data.testClockRolesAlgoMapping[AlgoBCString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoBCString])[Grandmaster] = 2

	// BC with slaves

	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesString])[BC1Slave] = 2
	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesString])[Grandmaster] = 3

	// Dual NIC BC
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCString])[BC1Slave] = 0
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCString])[Grandmaster] = 2
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCString])[BC2Master] = 3
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCString])[BC2Slave] = 4

	// Dual NIC BC with slaves

	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[BC1Slave] = 2
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[Grandmaster] = 3
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[BC2Slave] = 4
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[BC2Master] = 5
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesString])[Slave2] = 6

	// GM
	(*data.testClockRolesAlgoMapping[AlgoTelcoGMString])[Grandmaster] = 0
	(*data.testClockRolesAlgoMapping[AlgoTelcoGMString])[GrandmasterSibling] = 1

	// T-BC with local GM
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCString])[BC1Slave] = 0    // receiver
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCString])[BC1Master] = 1   // transmitter 1
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCString])[BC2Master] = 2   // transmitter 2
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCString])[Grandmaster] = 3 // local GM

	// T-BC with external GM
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCExtGMString])[BC1Slave] = 0  // receiver
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCExtGMString])[BC1Master] = 1 // transmitter 1
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCExtGMString])[BC2Master] = 2 // transmitter 2

	// T-BC with slaves and local GM
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesString])[Slave1] = 0      // downstream slave
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesString])[BC1Slave] = 1    // receiver
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesString])[BC1Master] = 2   // transmitter 1
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesString])[BC2Master] = 3   // transmitter 2
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesString])[Grandmaster] = 4 // local GM

	// T-BC with slaves and external GM
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesExtGMString])[Slave1] = 0    // downstream slave
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesExtGMString])[BC1Slave] = 1  // receiver
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesExtGMString])[BC1Master] = 2 // transmitter 1
	(*data.testClockRolesAlgoMapping[AlgoTelcoBCWithSlavesExtGMString])[BC2Master] = 3 // transmitter 2

	// OC, External GM
	(*data.testClockRolesAlgoMapping[AlgoOCExtGMString])[Slave1] = 0

	// Dual Follower, External GM
	(*data.testClockRolesAlgoMapping[AlgoDualFollowerExtGMString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoDualFollowerExtGMString])[Slave2] = 1

	// BC, External GM
	(*data.testClockRolesAlgoMapping[AlgoBCExtGMString])[BC1Slave] = 0
	(*data.testClockRolesAlgoMapping[AlgoBCExtGMString])[BC1Master] = 1

	// BC with slaves, External GM

	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesExtGMString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesExtGMString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoBCWithSlavesExtGMString])[BC1Slave] = 2

	// Dual NIC BC, External GM

	(*data.testClockRolesAlgoMapping[AlgoDualNicBCExtGMString])[BC1Slave] = 0
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCExtGMString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCExtGMString])[BC2Slave] = 2
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCExtGMString])[BC2Master] = 3

	// Dual NIC BC with slaves, External GM
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGMString])[Slave1] = 0
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGMString])[BC1Master] = 1
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGMString])[BC1Slave] = 2
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGMString])[BC2Slave] = 4
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGMString])[BC2Master] = 5
	(*data.testClockRolesAlgoMapping[AlgoDualNicBCWithSlavesExtGMString])[Slave2] = 6

	for _, name := range enabledProblems {
		// Initializing problems
		solver.GlobalConfig.InitProblem(
			name,
			*data.problems[name],
			*data.testClockRolesAlgoMapping[name],
		)

		// Solve problem
		solver.GlobalConfig.Run(name)
	}

	// print first solution
	solver.GlobalConfig.PrintFirstSolution()

	// store the solutions
	data.solutions = solver.GlobalConfig.GetSolutions()

	// update testconfig found solutions
	initFoundSolutions()
}

// Gets the discovered configuration
func GetFullDiscoveredConfig(namespace string, forceUpdate bool) TestConfig {
	logrus.Infof("Getting ptp configuration for namespace:%s", namespace)
	defer logrus.Infof("Current PTP test config=%s", &GlobalConfig)

	if GlobalConfig.Status == DiscoveryFailureStatus ||
		GlobalConfig.Status == DiscoverySuccessStatus && !forceUpdate {
		return GlobalConfig
	}

	discoverPTPConfiguration(namespace)
	return GlobalConfig
}

func CreatePtpConfigGrandMaster(nodeName, ifName string) error {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	// Labeling the grandmaster node
	_, err = nodes.LabelNode(nodeName, pkg.PtpGrandmasterNodeLabel, "")
	if err != nil {
		logrus.Errorf("Error setting Grandmaster node role label: %s", err)
	}

	// Grandmaster - add interface section with auth settings
	gmConfig := GetPtp4lConfigWithAuth(BasePtp4lConfig) + "\npriority1 0\npriority2 0\nclockClass 6"
	gmConfig = AddAuthSettings(AddInterface(gmConfig, ifName, 1))
	ptp4lsysOpts := ptp4lEthernet
	phc2sysOpts := phc2sysGM
	return createConfig(pkg.PtpGrandMasterPolicyName,
		&ifName,
		&ptp4lsysOpts,
		gmConfig,
		&phc2sysOpts,
		pkg.PtpGrandmasterNodeLabel,
		ptr.To(int64(defaultPriority)),
		ptpSchedulingPolicy,
		ptr.To(int64(defaultSchedulingPriority)),
	)
}

func CreatePtpConfigWPCGrandMaster(policyName string, nodeName string, ifList []string, deviceID string) error {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	// Sleep for a second to allow previous label on the same node to complete
	time.Sleep(time.Second)
	_, err = nodes.LabelNode(nodeName, pkg.PtpClockUnderTestNodeLabel, "")
	_, err = nodes.LabelNode(nodeName, pkg.PtpGrandmasterNodeLabel, "")
	if err != nil {
		logrus.Errorf("Error setting WPC GM node role label: %s", err)
	}

	ts2phcConfig := BaseTs2PhcConfig + fmt.Sprintf("\nts2phc.nmea_serialport  /dev/%s\n", deviceID)
	ts2phcConfig = fmt.Sprintf("%s\n[%s]\nts2phc.extts_polarity rising\nts2phc.extts_correction 0\n", ts2phcConfig, ifList[0])
	ptp4lConfig := GetPtp4lConfigWithAuth(BasePtp4lConfig) + "boundary_clock_jbod 1\n"
	ptp4lConfig = AddAuthSettings(AddInterface(ptp4lConfig, ifList[0], 1))
	ptp4lConfig = AddAuthSettings(AddInterface(ptp4lConfig, ifList[1], 1))
	ptp4lsysOpts := ptp4lEthernet
	ts2phcOpts := " "
	ph2sysOpts := fmt.Sprintf("-r -u 0 -m -N 8 -R 16 -s %s -n 24", ifList[0])

	// Get test configuration values for E810 plugin settings
	testParameters, err := ptptestconfig.GetPtpTestConfig()
	if err != nil {
		return fmt.Errorf("failed to get test config: %v", err)
	}

	// Get E810 plugin settings from environment variables (following existing pattern)
	// MAX_OFFSET_IN_NS = LocalMaxHoldoverOffset, MIN_OFFSET_IN_NS = -LocalMaxHoldoverOffset
	// HOLDOVER_TIMEOUT_S = LocalHoldoverTimeout (keep from YAML for now)
	// MAX_IN_SPEC_OFFSET_NS = MaxInSpecOffset
	localMaxHoldoverOffset := metrics.MaxOffsetNs
	localHoldoverTimeout := testParameters.GlobalConfig.HoldOverTimeout // Keep this from YAML for now
	maxInSpecOffset := metrics.MaxInSpecOffsetNs

	var plugins map[string]*apiextensions.JSON
	yamlData := fmt.Sprintf(`
  e810:
    enableDefaultConfig: false
    settings:
      LocalMaxHoldoverOffSet: %d
      LocalHoldoverTimeout: %d
      MaxInSpecOffset: %d
    pins:
      "$iface_master":
         "U.FL2": "0 2"
         "U.FL1": "0 1"
         "SMA2": "0 2"
         "SMA1": "0 1"
    ublxCmds:
      - args:
          - "-P"
          - "29.20"
          - "-z"
          - "CFG-HW-ANT_CFG_VOLTCTRL,1"
        reportOutput: false
      - args:
          - "-P"
          - "29.20"
          - "-e"
          - "GPS"
        reportOutput: false
      - args:
          - "-P"
          - "29.20"
          - "-d"
          - "Galileo"
        reportOutput: false
      - args:
          - "-P"
          - "29.20"
          - "-d"
          - "GLONASS"
        reportOutput: false
      - args:
          - "-P"
          - "29.20"
          - "-d"
          - "BeiDou"
        reportOutput: false
      - args:
          - "-P"
          - "29.20"
          - "-d"
          - "SBAS"
        reportOutput: false
      - args:
          - "-P"
          - "29.20"
          - "-t"
          - "-w"
          - "5"
          - "-v"
          - "1"
          - "-e"
          - "SURVEYIN,600,50000"
        reportOutput: true
      - args:
          - "-P"
          - "29.20"
          - "-p"
          - "MON-HW"
        reportOutput: true
      - args:
          - "-P"
          - "29.20"
          - "-p"
          - "CFG-MSG,1,38,248"
        reportOutput: true
`, localMaxHoldoverOffset, localHoldoverTimeout, maxInSpecOffset)

	// Unmarshal the YAML data into a generic map
	var genericMap map[string]interface{}
	err = yaml.Unmarshal([]byte(strings.Replace(yamlData, "$iface_master", ifList[0], -1)), &genericMap)
	if err != nil {
		logrus.Fatalf("error: %v", err)
	}

	// Marshal the generic map to JSON
	jsonData, err := json.Marshal(genericMap)
	if err != nil {
		logrus.Fatalf("error: %v", err)
	}

	// Unmarshal the JSON data into a map[string]*apiextensions.JSON
	result := make(map[string]*apiextensions.JSON)
	err = json.Unmarshal(jsonData, &result)
	if err != nil {
		logrus.Fatalf("error: %v", err)
	}
	plugins = result
	return createConfigWithTs2PhcAndPlugins(policyName,
		nil,
		&ptp4lsysOpts,
		ptp4lConfig,
		ts2phcConfig,
		&ph2sysOpts,
		pkg.PtpClockUnderTestNodeLabel,
		ptr.To(int64(defaultPriority)),
		ptpSchedulingPolicy,
		ptr.To(int64(defaultSchedulingPriority)),
		&ts2phcOpts,
		plugins)
}

func CreatePtpConfigTelcoBoundaryClock(configName, nodeName, ifSlaveName string, ifMasterNames []string, firstNicInterface string, deviceID string) error {
	ptpSchedulingPolicy := SCHED_FIFO
	// Sleep for a second to allow previous label on the same node to complete
	time.Sleep(time.Second)
	_, err := nodes.LabelNode(nodeName, pkg.PtpClockUnderTestNodeLabel, "")
	if err != nil {
		logrus.Errorf("Error setting T-BC node role label: %s", err)
	}

	tbcBase := BasePtp4lConfig
	tbcBase = strings.Replace(tbcBase, "announceReceiptTimeout 6", "announceReceiptTimeout 3", 1)
	tbcBase = strings.Replace(tbcBase, "verbose 1", "verbose 0", 1)
	tbcBase = strings.Replace(tbcBase, "summary_interval -4", "summary_interval 0", 1)
	tbcBase = strings.Replace(tbcBase, "clock_class_threshold 7", "clock_class_threshold 135", 1)
	tbcBase = strings.Replace(tbcBase, "pi_proportional_const 0.0", "pi_proportional_const 0.60", 1)
	tbcBase = strings.Replace(tbcBase, "pi_integral_const 0.0", "pi_integral_const 0.0003", 1)
	tbcBase = strings.Replace(tbcBase, "neighborPropDelayThresh 20000000", "neighborPropDelayThresh 20000000\nmasterOnly 0", 1)

	// T-BC receiver profile (tbc-tr)
	receiverConfig := tbcBase +
		"\nslaveOnly 0\npriority1 128\npriority2 128\ndomainNumber 24\nclockClass 248" +
		"\nboundary_clock_jbod 1\nclock_type OC\n"
	receiverConfig += fmt.Sprintf("[%s]\nmasterOnly 0\n", ifSlaveName)

	// T-BC transmitter profile (tbc-tt) - multiple transmitter interfaces
	transmitterConfig := tbcBase +
		"\nslaveOnly 0\npriority1 128\npriority2 128\ndomainNumber 24\nclockClass 248" +
		"\nboundary_clock_jbod 1\nclock_type BC\n"
	for _, ifMasterName := range ifMasterNames {
		transmitterConfig += fmt.Sprintf("[%s]\nmasterOnly 1\n", ifMasterName)
	}

	ptp4lsysOpts := ptp4lEthernet
	phc2sysOpts := fmt.Sprintf("-r -n 24 -N 8 -R 16 -u 0 -m -s %s", ifSlaveName)
	ts2phcOpts := "-s generic -a --ts2phc.rh_external_pps 1"

	// T-BC ts2phc configuration - use first port of the NIC
	ts2phcConfig := fmt.Sprintf(`[global]
use_syslog  0
verbose 1
logging_level 7
ts2phc.pulsewidth 100000000
leapfile  /usr/share/zoneinfo/leap-seconds.list
domainNumber 24
uds_address /var/run/ptp4l.0.socket
[%s]
ts2phc.extts_polarity rising
ts2phc.extts_correction -10
ts2phc.master 0`, firstNicInterface)

	// Get E810 plugin settings from environment variables (following existing pattern)
	// MAX_OFFSET_IN_NS = LocalMaxHoldoverOffset, MIN_OFFSET_IN_NS = -LocalMaxHoldoverOffset
	// HOLDOVER_TIMEOUT_S = LocalHoldoverTimeout (keep from YAML for now)
	// MAX_IN_SPEC_OFFSET_NS = MaxInSpecOffset
	testParameters, err := ptptestconfig.GetPtpTestConfig()
	if err != nil {
		return fmt.Errorf("failed to get test config: %v", err)
	}

	localMaxHoldoverOffset := metrics.MaxOffsetNs
	localHoldoverTimeout := testParameters.GlobalConfig.HoldOverTimeout // Keep this from YAML for now
	maxInSpecOffset := metrics.MaxInSpecOffsetNs

	// E810 plugin configuration for T-BC
	var plugins map[string]*apiextensions.JSON
	yamlData := fmt.Sprintf(`
  e810:
    enableDefaultConfig: false
    interconnections:
    - gnssInput: false
      id: "$iface_master"
      part: E810-XXVDA4T
      phaseOutputConnectors:
      - SMA1
      upstreamPort: "$iface_slave"
    settings:
      LocalMaxHoldoverOffSet: %d
      LocalHoldoverTimeout: %d
      MaxInSpecOffset: %d
    pins:
      "$iface_master":
         "U.FL2": "0 2"
         "U.FL1": "0 1"
         "SMA2": "2 2"
         "SMA1": "0 1"
`, localMaxHoldoverOffset, localHoldoverTimeout, maxInSpecOffset)

	// Unmarshal the YAML data into a generic map
	var genericMap map[string]interface{}
	interconnectYaml := strings.Replace(yamlData, "$iface_master", firstNicInterface, -1)
	interconnectYaml = strings.Replace(interconnectYaml, "$iface_slave", ifSlaveName, -1)
	err = yaml.Unmarshal([]byte(interconnectYaml), &genericMap)
	if err != nil {
		logrus.Fatalf("error: %v", err)
	}

	// Marshal the generic map to JSON
	jsonData, err := json.Marshal(genericMap)
	if err != nil {
		logrus.Fatalf("error: %v", err)
	}

	// Unmarshal the JSON data into a map[string]*apiextensions.JSON
	result := make(map[string]*apiextensions.JSON)
	err = json.Unmarshal(jsonData, &result)
	if err != nil {
		logrus.Fatalf("error: %v", err)
	}
	plugins = result

	// Create multi-profile PTP config for T-BC
	return createTelcoBCConfig(configName,
		receiverConfig,
		transmitterConfig,
		&ptp4lsysOpts,
		&phc2sysOpts,
		pkg.PtpClockUnderTestNodeLabel,
		ptr.To(int64(defaultPriority)),
		ptpSchedulingPolicy,
		ptr.To(int64(10)),
		ts2phcConfig,
		&ts2phcOpts,
		plugins)
}

func CreatePtpConfigBC(policyName, nodeName, ifMasterName, ifSlaveName string, phc2sys bool) (err error) {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	// Sleep for a second to allow previous label on the same node to complete
	time.Sleep(time.Second)
	_, err = nodes.LabelNode(nodeName, pkg.PtpClockUnderTestNodeLabel, "")
	if err != nil {
		logrus.Errorf("Error setting BC node role label: %s", err)
	}

	bcConfig := GetPtp4lConfigWithAuth(BasePtp4lConfig) + "\nboundary_clock_jbod 1\ngmCapable 0"
	bcConfig = AddAuthSettings(AddInterface(bcConfig, ifSlaveName, 0))
	bcConfig = AddAuthSettings(AddInterface(bcConfig, ifMasterName, 1))
	ptp4lsysOpts := ptp4lEthernet

	var phc2sysOpts *string
	temp := phc2sysSlave
	if phc2sys {
		phc2sysOpts = &temp
	} else {
		phc2sysOpts = nil
	}
	return createConfig(policyName,
		nil,
		&ptp4lsysOpts,
		bcConfig,
		phc2sysOpts,
		pkg.PtpClockUnderTestNodeLabel,
		ptr.To(int64(defaultPriority)),
		ptpSchedulingPolicy,
		ptr.To(int64(defaultSchedulingPriority)))
}

func CreatePtpConfigOC(profileName, nodeName, ifSlaveName string, phc2sys bool, label string) (err error) {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	// Sleep for a second to allow previous label on the same node to complete
	time.Sleep(time.Second)
	_, err = nodes.LabelNode(nodeName, label, "")
	if err != nil {
		logrus.Errorf("Error setting Slave node role label: %s", err)
	}
	ptp4lsysOpts := ptp4lEthernetSlave
	var phc2sysOpts *string
	temp := phc2sysSlave
	if phc2sys {
		phc2sysOpts = &temp
	} else {
		phc2sysOpts = nil
	}

	// Slave OC - add interface section with auth settings
	slaveConfig := GetPtp4lConfigWithAuth(BasePtp4lConfig)
	slaveConfig = AddAuthSettings(AddInterface(slaveConfig, ifSlaveName, 0))
	return createConfig(profileName,
		&ifSlaveName,
		&ptp4lsysOpts,
		slaveConfig,
		phc2sysOpts,
		label,
		ptr.To(int64(defaultPriority)),
		ptpSchedulingPolicy,
		ptr.To(int64(defaultSchedulingPriority)))
}

func CreatePtpConfigDualFollower(profileName, nodeName, ifSlave1Name, ifSlave2Name string, phc2sys bool, label string) (err error) {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	// Sleep for a second to allow previous label on the same node to complete
	time.Sleep(time.Second)
	_, err = nodes.LabelNode(nodeName, label, "")
	if err != nil {
		logrus.Errorf("Error setting Slave node role label: %s", err)
	}
	ptp4lsysOpts := ptp4lEthernetSlave
	var phc2sysOpts *string
	temp := phc2sysSlave
	if phc2sys {
		phc2sysOpts = &temp
	} else {
		phc2sysOpts = nil
	}

	ptp4lDualFollowerConfig := GetPtp4lConfigWithAuth(BasePtp4lConfig) + "\nslaveOnly 1"
	ptp4lDualFollowerConfig = AddAuthSettings(AddInterface(ptp4lDualFollowerConfig, ifSlave1Name, 0))
	ptp4lDualFollowerConfig = AddAuthSettings(AddInterface(ptp4lDualFollowerConfig, ifSlave2Name, 0))

	return createConfig(profileName,
		nil,
		&ptp4lsysOpts,
		ptp4lDualFollowerConfig,
		phc2sysOpts,
		label,
		ptr.To(int64(defaultPriority)),
		ptpSchedulingPolicy,
		ptr.To(int64(defaultSchedulingPriority)))
}

func PtpConfigOC(isExtGM bool) error {
	var grandmaster, slave1 int

	BestSolution := ""

	if isExtGM {
		if len(*data.solutions[AlgoOCExtGMString]) != 0 {
			BestSolution = AlgoOCExtGMString
		} else {
			return fmt.Errorf("no solution found for OC configuration in External GM mode")
		}
	} else {
		if len(*data.solutions[AlgoOCString]) != 0 {
			BestSolution = AlgoOCString
		}
		if BestSolution == "" {
			return fmt.Errorf("no solution found for OC configuration in Local GM mode")
		}
	}
	logrus.Infof("Configuring best solution= %s", BestSolution)
	switch BestSolution {
	case AlgoOCString:
		grandmaster = (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]

		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, true, pkg.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}

	case AlgoOCExtGMString:

		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]

		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]

		err := CreatePtpConfigOC(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, true, pkg.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
	}
	return nil
}
func PtpConfigDualFollower(isExtGM bool) error {
	var grandmaster, slave1, slave2 int

	BestSolution := ""

	if isExtGM {
		if len(*data.solutions[AlgoDualFollowerExtGMString]) != 0 {
			BestSolution = AlgoDualFollowerExtGMString
		} else {
			return fmt.Errorf("no solution found for Dual Follower configuration in External GM mode")
		}
	} else {
		if len(*data.solutions[AlgoDualFollowerString]) != 0 {
			BestSolution = AlgoDualFollowerString
		}
		if BestSolution == "" {
			return fmt.Errorf("no solution found for Dual Follower configuration in Local GM mode")
		}
	}
	logrus.Infof("Configuring best solution= %s", BestSolution)
	switch BestSolution {
	case AlgoDualFollowerString:
		grandmaster = (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]
		slave2 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave2]
		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]
		slave2If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave2]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigDualFollower(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, slave2If.IfName, true, pkg.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}

	case AlgoDualFollowerExtGMString:

		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]
		slave2 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave2]

		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]
		slave2If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave2]]

		err := CreatePtpConfigDualFollower(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, slave2If.IfName, true, pkg.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
	}
	return nil
}

func PtpConfigBC(isExtGM bool) error {
	var grandmaster, bc1Master, bc1Slave, slave1 int

	BestSolution := ""

	if isExtGM {
		if len(*data.solutions[AlgoBCExtGMString]) != 0 {
			BestSolution = AlgoBCExtGMString
		}
		if len(*data.solutions[AlgoBCWithSlavesExtGMString]) != 0 {
			BestSolution = AlgoBCWithSlavesExtGMString
		}
		if BestSolution == "" {
			return fmt.Errorf("no solution found for BC configuration in External GM mode")
		}

	} else {
		if len(*data.solutions[AlgoBCString]) != 0 {
			BestSolution = AlgoBCString
		}
		if len(*data.solutions[AlgoBCWithSlavesString]) != 0 {
			BestSolution = AlgoBCWithSlavesString
		}
		if BestSolution == "" {
			return fmt.Errorf("no solution found for BC configuration in Local GM mode")
		}
	}

	logrus.Infof("Configuring best solution= %s", BestSolution)
	switch BestSolution {
	case AlgoBCWithSlavesString:
		grandmaster = (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]

		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, false, pkg.PtpSlave1NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}

	case AlgoBCString:
		grandmaster = (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]

		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

	case AlgoBCExtGMString:

		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]

		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]

		err := CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}
	case AlgoBCWithSlavesExtGMString:
		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]

		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]

		err := CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, false, pkg.PtpSlave1NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
	}
	return nil
}

// createPtpConfigPhc2SysHA creates a PTP configuration that only handles phc2sys for HA profile management
func createPtpConfigPhc2SysHA(policyName string, nodeName string, haProfiles []string) error {
	// Sleep for a second to allow previous label on the same node to complete
	time.Sleep(time.Second)

	clockUnderTestNodeLabel := pkg.PtpClockUnderTestNodeLabel
	_, err := nodes.LabelNode(nodeName, clockUnderTestNodeLabel, "")
	if err != nil {
		return fmt.Errorf("error setting HA node role label: %s", err)
	}

	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}

	phc2sysOpts := phc2sysDualNicBCHA
	ptp4lOpts := "" // no ptp4l options

	ptpProfile := ptpv1.PtpProfile{
		Name:                  &policyName,
		Phc2sysOpts:           &phc2sysOpts,
		Ptp4lOpts:             &ptp4lOpts,
		PtpSchedulingPolicy:   &ptpSchedulingPolicy,
		PtpSchedulingPriority: ptr.To(int64(65)),
		PtpSettings:           map[string]string{"haProfiles": strings.Join(haProfiles, ",")},
	}

	ptpRecommend := ptpv1.PtpRecommend{
		Profile:  &policyName,
		Priority: ptr.To(int64(5)),
		Match:    []ptpv1.MatchRule{{NodeLabel: &clockUnderTestNodeLabel}},
	}

	policy := ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{Profile: []ptpv1.PtpProfile{ptpProfile},
			Recommend: []ptpv1.PtpRecommend{ptpRecommend}}}

	_, err = client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &policy, metav1.CreateOptions{})
	return err
}

// CreatePtpConfigWithUnderscoreProfileNames creates a PtpConfig with profile names containing underscores
// to verify that the webhook accepts underscores in profile names, especially for HA profiles.
// The metadata.name uses hyphens (Kubernetes requirement), but spec.profile[].name uses underscores.
func CreatePtpConfigWithUnderscoreProfileNames() error {
	policyName := pkg.PtpUnderscoreTestPolicyName
	// Profile names with underscores (this is what we're testing)
	profileName1 := "test_profile_bc1"
	profileName2 := "test_profile_bc2"
	haProfileName := "test_ha_profile"

	ptpSchedulingPolicy := SCHED_OTHER
	phc2sysOpts := "-a -r -n 24"
	ptp4lOpts := ""

	// Create HA profile with underscore names in haProfiles setting
	haProfile := ptpv1.PtpProfile{
		Name:                  &haProfileName,
		Phc2sysOpts:           &phc2sysOpts,
		Ptp4lOpts:             &ptp4lOpts,
		PtpSchedulingPolicy:   &ptpSchedulingPolicy,
		PtpSchedulingPriority: ptr.To(int64(65)),
		// haProfiles contains profile names with underscores
		PtpSettings: map[string]string{"haProfiles": profileName1 + "," + profileName2},
	}

	ptpRecommend := ptpv1.PtpRecommend{
		Profile:  &haProfileName,
		Priority: ptr.To(int64(5)),
	}

	policy := ptpv1.PtpConfig{
		ObjectMeta: metav1.ObjectMeta{Name: policyName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{
			Profile:   []ptpv1.PtpProfile{haProfile},
			Recommend: []ptpv1.PtpRecommend{ptpRecommend},
		},
	}

	_, err := client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &policy, metav1.CreateOptions{})
	return err
}

// DeletePtpConfigWithUnderscoreProfileNames deletes the test PtpConfig created for underscore profile name testing
func DeletePtpConfigWithUnderscoreProfileNames() error {
	policyName := pkg.PtpUnderscoreTestPolicyName
	err := client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Delete(context.Background(), policyName, metav1.DeleteOptions{})
	return err
}

func PtpConfigDualNicBC(isExtGM bool, phc2SysHaEnabled bool) error {
	var grandmaster, bc1Master, bc1Slave, slave1, bc2Master, bc2Slave, slave2 int

	BestSolution := ""
	if isExtGM {
		if len(*data.solutions[AlgoDualNicBCExtGMString]) != 0 {
			BestSolution = AlgoDualNicBCExtGMString
		}
		if len(*data.solutions[AlgoDualNicBCWithSlavesExtGMString]) != 0 {
			BestSolution = AlgoDualNicBCWithSlavesExtGMString
		}
		if BestSolution == "" {
			return fmt.Errorf("no solution found for Dual NIC BC configuration in External GM mode")
		}
	} else {
		if len(*data.solutions[AlgoDualNicBCString]) != 0 {
			BestSolution = AlgoDualNicBCString
		}
		if len(*data.solutions[AlgoDualNicBCWithSlavesString]) != 0 {
			BestSolution = AlgoDualNicBCWithSlavesString
		}
		if BestSolution == "" {
			return fmt.Errorf("no solution found for Dual NIC BC configuration in Local GM mode")
		}
	}

	logrus.Infof("Configuring best solution= %s", BestSolution)
	switch BestSolution {
	case AlgoDualNicBCWithSlavesString:
		grandmaster = (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]
		bc2Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Master]
		bc2Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Slave]
		slave2 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave2]

		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]
		bc2MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Master]]
		bc2SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Slave]]
		slave2If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave2]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, !phc2SysHaEnabled)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, false, pkg.PtpSlave1NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
		err = CreatePtpConfigOC(pkg.PtpSlave2PolicyName, slave2If.NodeName,
			slave2If.IfName, false, pkg.PtpSlave2NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave2 ptpconfig: %s", err)
		}

	case AlgoDualNicBCString:
		grandmaster = (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
		bc2Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Master]
		bc2Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Slave]

		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
		bc2MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Master]]
		bc2SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Slave]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, !phc2SysHaEnabled)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}

	case AlgoDualNicBCExtGMString:

		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
		bc2Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Master]
		bc2Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Slave]

		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
		bc2MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Master]]
		bc2SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Slave]]

		err := CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, !phc2SysHaEnabled)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}

	case AlgoDualNicBCWithSlavesExtGMString:
		bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
		bc1Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
		slave1 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave1]
		bc2Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Master]
		bc2Slave = (*data.testClockRolesAlgoMapping[BestSolution])[BC2Slave]
		slave2 = (*data.testClockRolesAlgoMapping[BestSolution])[Slave2]

		bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
		bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
		slave1If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave1]]
		bc2MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Master]]
		bc2SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Slave]]
		slave2If := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][slave2]]

		err := CreatePtpConfigBC(pkg.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, !phc2SysHaEnabled)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(pkg.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(pkg.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, false, pkg.PtpSlave1NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
		err = CreatePtpConfigOC(pkg.PtpSlave2PolicyName, slave2If.NodeName,
			slave2If.IfName, false, pkg.PtpSlave2NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave2 ptpconfig: %s", err)
		}
	}

	// Create the third HA-specific phc2sys config if HA is enabled
	if phc2SysHaEnabled {
		logrus.Infof("Creating HA ptpconfig")
		// Determine the node for the HA config - use the same node as BC1
		var haNodeName string
		switch BestSolution {
		case AlgoDualNicBCWithSlavesString, AlgoDualNicBCString:
			bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
			bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
			haNodeName = bc1MasterIf.NodeName
		case AlgoDualNicBCExtGMString, AlgoDualNicBCWithSlavesExtGMString:
			bc1Master = (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
			bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
			haNodeName = bc1MasterIf.NodeName
		}

		// Create HA config with profiles from the two BC configs
		haProfiles := []string{pkg.PtpBcMaster1PolicyName, pkg.PtpBcMaster2PolicyName}
		err := createPtpConfigPhc2SysHA(pkg.PtpDualNicBCHAPolicyName, haNodeName, haProfiles)
		if err != nil {
			return fmt.Errorf("failed to create HA ptpconfig: %v", err)
		}
	}

	return nil
}

func PtpConfigTelcoGM(isExtGM bool) error {
	BestSolution := ""
	if len(*data.solutions[AlgoTelcoGMString]) != 0 {
		BestSolution = AlgoTelcoGMString
	}
	switch BestSolution {
	case AlgoTelcoGMString:
		solution := (*data.solutions[BestSolution])[FirstSolution]
		gm0 := (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		gm1 := (*data.testClockRolesAlgoMapping[BestSolution])[GrandmasterSibling]

		gmIf0 := GlobalConfig.L2Config.GetPtpIfList()[solution[gm0]]
		gmIf1 := GlobalConfig.L2Config.GetPtpIfList()[solution[gm1]]

		ifList := []string{gmIf0.IfName, gmIf1.IfName}

		deviceID := gmIf0.IfPTPCaps.GnssDevice
		if deviceID == "" {
			deviceID = gmIf1.IfPTPCaps.GnssDevice
		}

		err := CreatePtpConfigWPCGrandMaster(pkg.PtpWPCGrandMasterPolicyName, gmIf0.NodeName, ifList, deviceID)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}
	}
	return nil
}

func PtpConfigTelcoBC(isExtGM bool) error {
	// Select the appropriate solution based on whether external GM is used
	BestSolution := AlgoTelcoBCString
	if isExtGM {
		BestSolution = AlgoTelcoBCExtGMString
	}

	if len(*data.solutions[BestSolution]) == 0 {
		return fmt.Errorf("no T-BC solution found for %s", BestSolution)
	}

	// Get interface indices
	bc1Slave := (*data.testClockRolesAlgoMapping[BestSolution])[BC1Slave]
	bc1Master := (*data.testClockRolesAlgoMapping[BestSolution])[BC1Master]
	bc2Master := (*data.testClockRolesAlgoMapping[BestSolution])[BC2Master]

	bc1SlaveIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Slave]]
	bc1MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc1Master]]
	bc2MasterIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][bc2Master]]

	// Ensure the leading (first) interface is on the same NIC as the slave interface,
	// using PTP capabilities collected by l2discovery-lib.
	IfList, firstNicInterface, deviceID, err := ptphelper.InterfacesByPHCAndPins(GlobalConfig.L2Config, bc1SlaveIf.NodeName, bc1SlaveIf.IfName)
	if err != nil {
		return err
	}

	if len(IfList) < 2 {
		logrus.Error("T-BC requires at least 2 interfaces on WPC NIC for slave interface")
		return fmt.Errorf("T-BC requires at least 2 interfaces on WPC NIC for slave interface, found %d", len(IfList))
	}

	// Verify all interfaces are on the same NIC
	if bc1SlaveIf.NodeName != bc1MasterIf.NodeName || bc1SlaveIf.NodeName != bc2MasterIf.NodeName {
		return fmt.Errorf("all T-BC interfaces must be on the same node")
	}

	// Create T-BC configuration
	transmitterIfs := []string{bc1MasterIf.IfName, bc2MasterIf.IfName}
	err = CreatePtpConfigTelcoBoundaryClock(pkg.PTPWPCTBCPolicyName,
		bc1SlaveIf.NodeName, bc1SlaveIf.IfName, transmitterIfs, firstNicInterface, deviceID)
	if err != nil {
		logrus.Errorf("Error creating T-BC ptpconfig: %s", err)
		return err
	}

	// Create local Grandmaster if not using external GM
	if !isExtGM {
		grandmaster := (*data.testClockRolesAlgoMapping[BestSolution])[Grandmaster]
		gmIf := GlobalConfig.L2Config.GetPtpIfList()[(*data.solutions[BestSolution])[FirstSolution][grandmaster]]
		err = CreatePtpConfigGrandMaster(gmIf.NodeName, gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating local Grandmaster ptpconfig: %s", err)
			return err
		}
	}

	return nil
}

// helper function to add an interface to the ptp4l config
func AddInterface(ptpConfig, iface string, masterOnly int) (updatedPtpConfig string) {
	return fmt.Sprintf("%s\n[%s]\nmasterOnly %d", ptpConfig, iface, masterOnly)
}

// AddAuthSettings appends spp and active_key_id to the config if auth is enabled
// Use this after adding interface sections to add auth settings
func AddAuthSettings(ptpConfig string) string {
	if IsAuthEnabled() {
		return ptpConfig + "\nspp 1\nactive_key_id 1"
	}
	return ptpConfig
}

// createTelcoBCConfig creates a multi-profile PTP config for Telco Boundary Clock
// with separate receiver (tbc-tr) and transmitter (tbc-tt) profiles
func createTelcoBCConfig(configName string, receiverConfig, transmitterConfig string, ptp4lOpts, phc2sysOpts *string, nodeLabel string, priority *int64, ptpSchedulingPolicy string, ptpSchedulingPriority *int64, ts2phcConfig string, ts2phcOpts *string, plugins map[string]*apiextensions.JSON) error {
	// Create receiver profile (tbc-tr)
	receiverProfileName := "tbc-tr"
	receiverProfile := ptpv1.PtpProfile{
		Name:                  &receiverProfileName,
		Interface:             nil,
		Phc2sysOpts:           phc2sysOpts,
		Ptp4lOpts:             ptp4lOpts,
		PtpSchedulingPolicy:   &ptpSchedulingPolicy,
		PtpSchedulingPriority: ptpSchedulingPriority,
		Ts2PhcOpts:            ts2phcOpts,
		Plugins:               plugins,
		PtpSettings: map[string]string{
			"inSyncConditionThreshold": "10",
			"inSyncConditionTimes":     "12",
			"logReduce":                "false",
		},
	}
	if receiverConfig != "" {
		receiverProfile.Ptp4lConf = &receiverConfig
	}
	if ts2phcConfig != "" {
		receiverProfile.Ts2PhcConf = &ts2phcConfig
	}

	// Create transmitter profile (tbc-tt)
	transmitterProfileName := "tbc-tt"
	transmitterProfile := ptpv1.PtpProfile{
		Name:                  &transmitterProfileName,
		Interface:             nil,
		Phc2sysOpts:           nil, // No phc2sys for transmitter
		Ptp4lOpts:             ptp4lOpts,
		PtpSchedulingPolicy:   &ptpSchedulingPolicy,
		PtpSchedulingPriority: ptpSchedulingPriority,
		Plugins:               nil,
		PtpSettings:           map[string]string{"logReduce": "false", "controllingProfile": receiverProfileName},
	}
	if transmitterConfig != "" {
		transmitterProfile.Ptp4lConf = &transmitterConfig
	}

	matchRule := ptpv1.MatchRule{NodeLabel: &nodeLabel}
	ptpRecommendTR := ptpv1.PtpRecommend{Profile: &receiverProfileName, Priority: priority, Match: []ptpv1.MatchRule{matchRule}}
	ptpRecommendTT := ptpv1.PtpRecommend{Profile: &transmitterProfileName, Priority: priority, Match: []ptpv1.MatchRule{matchRule}}

	ptpConfig := ptpv1.PtpConfig{
		ObjectMeta: metav1.ObjectMeta{Name: configName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{
			Profile:   []ptpv1.PtpProfile{receiverProfile, transmitterProfile},
			Recommend: []ptpv1.PtpRecommend{ptpRecommendTR, ptpRecommendTT},
		},
	}

	_, err := client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &ptpConfig, metav1.CreateOptions{})
	return err
}

func createConfigWithTs2PhcAndPlugins(profileName string, ifaceName, ptp4lOpts *string, ptp4lConfig string, ts2phcConfig string, phc2sysOpts *string, nodeLabel string, priority *int64, ptpSchedulingPolicy string, ptpSchedulingPriority *int64, ts2phcOpts *string, plugins map[string]*apiextensions.JSON) error {
	thresholds := ptpv1.PtpClockThreshold{}

	testParameters, err := ptptestconfig.GetPtpTestConfig()
	if err != nil {
		return fmt.Errorf("failed to get test config: %v", err)
	}
	thresholds.MaxOffsetThreshold = int64(testParameters.GlobalConfig.MaxOffset)
	thresholds.MinOffsetThreshold = int64(testParameters.GlobalConfig.MinOffset)
	ptpProfile := ptpv1.PtpProfile{Name: &profileName, Interface: ifaceName, Phc2sysOpts: phc2sysOpts, Ptp4lOpts: ptp4lOpts, PtpSchedulingPolicy: &ptpSchedulingPolicy, PtpSchedulingPriority: ptpSchedulingPriority,
		PtpClockThreshold: &thresholds, Ts2PhcOpts: ts2phcOpts, Plugins: plugins, PtpSettings: map[string]string{"logReduce": "false"}}
	if ptp4lConfig != "" {
		ptpProfile.Ptp4lConf = &ptp4lConfig
	}
	if ts2phcConfig != "" {
		ptpProfile.Ts2PhcConf = &ts2phcConfig
	}
	matchRule := ptpv1.MatchRule{NodeLabel: &nodeLabel}
	ptpRecommend := ptpv1.PtpRecommend{Profile: &profileName, Priority: priority, Match: []ptpv1.MatchRule{matchRule}}

	policy := ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: profileName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{Profile: []ptpv1.PtpProfile{ptpProfile},
			Recommend: []ptpv1.PtpRecommend{ptpRecommend}}}
	_, err = client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &policy, metav1.CreateOptions{})
	return err
}

// helper function to create a ptpconfig
func createConfig(profileName string, ifaceName, ptp4lOpts *string, ptp4lConfig string, phc2sysOpts *string, nodeLabel string, priority *int64, ptpSchedulingPolicy string, ptpSchedulingPriority *int64) error {
	thresholds := ptpv1.PtpClockThreshold{}

	testParameters, err := ptptestconfig.GetPtpTestConfig()
	if err != nil {
		return fmt.Errorf("failed to get test config: %v", err)
	}
	thresholds.MaxOffsetThreshold = int64(testParameters.GlobalConfig.MaxOffset)
	thresholds.MinOffsetThreshold = int64(testParameters.GlobalConfig.MinOffset)
	thresholds.HoldOverTimeout = int64(testParameters.GlobalConfig.HoldOverTimeout)

	if testParameters.GlobalConfig.DisableAllSlaveRTUpdate && nodeLabel != pkg.PtpGrandmasterNodeLabel && phc2sysOpts != nil {
		temp := "-v"
		phc2sysOpts = &temp
	}

	ptpProfile := ptpv1.PtpProfile{Name: &profileName, Interface: ifaceName, Phc2sysOpts: phc2sysOpts, Ptp4lOpts: ptp4lOpts, PtpSchedulingPolicy: &ptpSchedulingPolicy, PtpSchedulingPriority: ptpSchedulingPriority,
		PtpClockThreshold: &thresholds}

	if ptp4lConfig != "" {
		ptpProfile.Ptp4lConf = &ptp4lConfig
	}
	matchRule := ptpv1.MatchRule{NodeLabel: &nodeLabel}
	ptpRecommend := ptpv1.PtpRecommend{Profile: &profileName, Priority: priority, Match: []ptpv1.MatchRule{matchRule}}

	policy := ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: profileName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{Profile: []ptpv1.PtpProfile{ptpProfile},
			Recommend: []ptpv1.PtpRecommend{ptpRecommend}}}

	_, err = client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &policy, metav1.CreateOptions{})
	return err
}

// Discovers the PTP configuration
func discoverPTPConfiguration(namespace string) {
	var ptpConfigClockUnderTest []*ptpv1.PtpConfig

	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logrus.Errorf("error getting ptpconfig list, err=%s", err)
	}
	logrus.Infof("%d ptpconfig objects recovered", len(configList.Items))
	resetConfig()
	for profileIndex := range configList.Items {
		for _, r := range configList.Items[profileIndex].Spec.Recommend {
			for _, m := range r.Match {
				if m.NodeLabel == nil {
					continue
				}
				if *m.NodeLabel == pkg.PtpClockUnderTestNodeLabel {
					ptpConfigClockUnderTest = append(ptpConfigClockUnderTest, &configList.Items[profileIndex])
				}

				// Grand master, slave 1 and slave 2 are checked as they are always created by the test program
				if GlobalConfig.PtpModeDesired != Discovery && GlobalConfig.PtpModeDesired != None {
					if *m.NodeLabel == pkg.PtpGrandmasterNodeLabel {
						GlobalConfig.DiscoveredGrandMasterPtpConfig = (*ptpDiscoveryRes)(&configList.Items[profileIndex])
					}
					if *m.NodeLabel == pkg.PtpSlave1NodeLabel {
						GlobalConfig.DiscoveredSlave1PtpConfig = (*ptpDiscoveryRes)(&configList.Items[profileIndex])
					}
					if *m.NodeLabel == pkg.PtpSlave2NodeLabel {
						GlobalConfig.DiscoveredSlave2PtpConfig = (*ptpDiscoveryRes)(&configList.Items[profileIndex])
					}
				}
			}
		}
	}
	discoverMode(ptpConfigClockUnderTest)
}

func resetConfig() {
	GlobalConfig.Status = DiscoveryFailureStatus
	GlobalConfig.DiscoveredClockUnderTestPod = nil
	GlobalConfig.DiscoveredClockUnderTestPtpConfig = nil
	GlobalConfig.DiscoveredClockUnderTestSecondaryPtpConfig = nil
	GlobalConfig.DiscoveredSlave1PtpConfig = nil
	GlobalConfig.DiscoveredSlave2PtpConfig = nil
	GlobalConfig.DiscoveredGrandMasterPtpConfig = nil
}

// Helper function analysing ptpconfig to deduce the actual ptp configuration
func discoverMode(ptpConfigClockUnderTest []*ptpv1.PtpConfig) {
	GlobalConfig.Status = DiscoveryFailureStatus

	if len(ptpConfigClockUnderTest) == 0 {
		logrus.Warnf("No Configs present, cannot discover")
		return
	}
	numBc := 0
	numSecondaryBC := 0
	numPhc2SysHa := 0
	var allMasterIfs []string
	var allFollowerIfs []string
	logrus.Infof("Number of ptpconfigs under test: %d", len(ptpConfigClockUnderTest))
	for _, ptpConfig := range ptpConfigClockUnderTest {
		logrus.Infof("Analyzing ptpconfig: %s", ptpConfig.Name)

		masterIfStrings := ptpv1.GetInterfaces(*ptpConfig, ptpv1.Master)
		masterIfCount := len(masterIfStrings)
		followerIfStrings := ptpv1.GetInterfaces(*ptpConfig, ptpv1.Slave)
		slaveIfCount := len(followerIfStrings)

		allMasterIfs = append(allMasterIfs, masterIfStrings...)
		allFollowerIfs = append(allFollowerIfs, followerIfStrings...)

		// OC
		if masterIfCount == 0 && slaveIfCount == 1 && len(ptpConfigClockUnderTest) == 1 {
			GlobalConfig.PtpModeDiscovered = OrdinaryClock
			GlobalConfig.Status = DiscoverySuccessStatus
			GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			break
		}
		// Dual Follower
		if masterIfCount == 0 && slaveIfCount == 2 && len(ptpConfigClockUnderTest) == 1 {
			GlobalConfig.PtpModeDiscovered = DualFollowerClock
			GlobalConfig.Status = DiscoverySuccessStatus
			GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			break
		}
		logrus.Infof("ptptConfig: %s, masterIfCount: %d, slaveIfCount: %d", ptpConfig.Name, masterIfCount, slaveIfCount)
		// BC, Dual NIC BC and Dual NIC BC HA
		if masterIfCount >= 1 && slaveIfCount >= 1 {
			if numBc == 0 {
				GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			}
			if numBc == 1 {
				GlobalConfig.DiscoveredClockUnderTestSecondaryPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			}
			numBc++
			if ptphelper.IsSecondaryBc(ptpConfig) {
				numSecondaryBC++
			}
		} else if ptphelper.ConfigIsPhc2SysHa(ptpConfig) {
			numPhc2SysHa++
		}

		// T-BC state: Check for two profiles (tbc-tr and tbc-tt)
		if len(ptpConfig.Spec.Profile) == 2 {
			hasTbcTr := false
			hasTbcTt := false
			for _, profile := range ptpConfig.Spec.Profile {
				if profile.Name != nil {
					q := ptphelper.QualifyProfileName(ptpConfig.Name, *profile.Name)
					if q == ptphelper.QualifyProfileName(ptpConfig.Name, "tbc-tr") {
						hasTbcTr = true
					} else if q == ptphelper.QualifyProfileName(ptpConfig.Name, "tbc-tt") {
						hasTbcTt = true
					}
				}
			}
			if hasTbcTr && hasTbcTt {
				GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
				GlobalConfig.PtpModeDiscovered = TelcoBoundaryClock
				GlobalConfig.Status = DiscoverySuccessStatus
				logrus.Info("Detected T-BC configuration with tbc-tr and tbc-tt profiles")
				continue
			}
		}

		//WPC GM state
		if masterIfCount >= 2 && slaveIfCount == 0 && !strings.EqualFold(*ptpConfig.Spec.Profile[0].Ts2PhcConf, "") {

			GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			GlobalConfig.PtpModeDiscovered = TelcoGrandMasterClock
			GlobalConfig.Status = DiscoverySuccessStatus
			GlobalConfig.DiscoveredGrandMasterPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
		}
	}
	logrus.Infof("BCs found: %d, SecondaryBCs found: %d, Phc2Sys HA configs found: %d", numBc, numSecondaryBC, numPhc2SysHa)
	switch numBc {
	case 1:
		GlobalConfig.PtpModeDiscovered = BoundaryClock
		GlobalConfig.Status = DiscoverySuccessStatus
	case 2:
		switch numSecondaryBC {
		case 1:
			GlobalConfig.PtpModeDiscovered = DualNICBoundaryClock
			GlobalConfig.Status = DiscoverySuccessStatus
		case 2:
			if numPhc2SysHa == 1 {
				GlobalConfig.PtpModeDiscovered = DualNICBoundaryClockHA
				GlobalConfig.Status = DiscoverySuccessStatus
			}
		}
	}

	pod, err := ptphelper.GetPTPPodWithPTPConfig((*ptpv1.PtpConfig)(GlobalConfig.DiscoveredClockUnderTestPtpConfig))
	if err != nil {
		logrus.Errorf("Could not determine ptp daemon pod selected by ptpconfig: %s", err)
		GlobalConfig.Status = DiscoveryFailureStatus
	}
	GlobalConfig.DiscoveredClockUnderTestPod = pod
	GlobalConfig.DiscoveredFollowerInterfaces = allFollowerIfs
	GlobalConfig.DiscoveredMasterInterfaces = allMasterIfs
}

func GetPodsRunningPTP4l(fullConfig *TestConfig) (podList []*v1core.Pod, err error) {
	allPTPConfigs := []*ptpv1.PtpConfig{}

	allPTPConfigs = append(allPTPConfigs,
		(*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestPtpConfig),
		(*ptpv1.PtpConfig)(fullConfig.DiscoveredClockUnderTestSecondaryPtpConfig),
		(*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave1PtpConfig),
		(*ptpv1.PtpConfig)(fullConfig.DiscoveredSlave2PtpConfig),
		(*ptpv1.PtpConfig)(fullConfig.DiscoveredGrandMasterPtpConfig),
	)

	podNames := []string{}
	for _, aPTPConfig := range allPTPConfigs {
		if aPTPConfig == nil {
			continue
		}
		var aPod *v1core.Pod
		aPod, err = ptphelper.GetPTPPodWithPTPConfig(aPTPConfig)
		if err != nil {
			return podList, fmt.Errorf("could not determine pod managing this ptpconfig, err: %v", err)
		}
		if aPod == nil {
			return podList, fmt.Errorf("could not determine pod managing this ptpconfig, err: pod not found")
		}
		podList = append(podList, aPod)
		podNames = append(podNames, aPod.Name)
	}
	logrus.Infof("List of pods running ptp4l: %v", podNames)
	return podList, nil
}

// CreateSecurityMismatchSecret creates a secret with mismatched keys for authentication negative testing.
// When GM uses this secret (spp 0) and slaves use ptp-security-conf (spp 1), authentication will fail.
func CreateSecurityMismatchSecret(namespace string) *v1core.Secret {
	return &v1core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkg.PtpSecurityMismatchSecretName,
			Namespace: namespace,
		},
		StringData: map[string]string{
			"ptp-security.conf": `[security_association]
spp 0
1 AES128 HEX:0000000000000000000000000000000000000000000000000000000000000000
2 SHA256-128 HEX:FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF
`,
		},
	}
}

// CreateTestSecretForVolumeMountTest creates a test secret for volume mount cleanup testing
func CreateTestSecretForVolumeMountTest(namespace string) *v1core.Secret {
	return &v1core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkg.PtpTestVolumeSecretName,
			Namespace: namespace,
		},
		StringData: map[string]string{
			"test-key.conf": `[security_association]
spp 1
seqid_window 20
1 AES128 HEX:FAF48EBA01E7C5966A76CB787AED4E7B
2 SHA256-128 HEX:F8ADC6B8B8E9AA709106BA42481EC9E29607334DE2C3C737A11A12931DB27F8C
`,
		},
	}
}

// CreatePtpConfigForVolumeMountTest creates a PtpConfig for volume mount cleanup testing
func CreatePtpConfigForVolumeMountTest(nodeName string, interfaceName string) *ptpv1.PtpConfig {
	name := pkg.PtpVolumeMountCleanPolicyName
	priority := int64(10)
	ptp4lOpts := "-2"
	phc2sysOpts := fmt.Sprintf("-a -r -r -n 24 -N 8 -R 16 -s %s -m", interfaceName)

	ptp4lConf := fmt.Sprintf(`[global]
sa_file /etc/ptp-secret-mount/%s/test-key.conf
spp -1
logging_level 6
summary_interval 0

[%s]
masterOnly 0
spp 1
active_key_id 1
`, pkg.PtpTestVolumeSecretName, interfaceName)

	return &ptpv1.PtpConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: PtpLinuxDaemonNamespace,
		},
		Spec: ptpv1.PtpConfigSpec{
			Profile: []ptpv1.PtpProfile{
				{
					Name:        &name,
					Interface:   &interfaceName,
					Ptp4lOpts:   &ptp4lOpts,
					Phc2sysOpts: &phc2sysOpts,
					Ptp4lConf:   &ptp4lConf,
				},
			},
			Recommend: []ptpv1.PtpRecommend{
				{
					Profile:  &name,
					Priority: &priority,
					Match: []ptpv1.MatchRule{
						{
							NodeName: &nodeName,
						},
					},
				},
			},
		},
	}
}
