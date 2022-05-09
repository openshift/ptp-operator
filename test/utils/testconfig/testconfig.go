package testconfig

import (
	"fmt"
	"os"
	"strings"

	"context"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// OrdinaryClockString matches the OC clock mode in Environement
	OrdinaryClockString = "OC"
	// BoundaryClockString matches the BC clock mode in Environement
	BoundaryClockString = "BC"
	// DualNICBoundaryClockString matches the DualNICBC clock mode in Environement
	DualNICBoundaryClockString = "DualNICBC"
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
)

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
	// Discovery Discovery mode
	Discovery
	// None inital empty mode
	None
)

type TestConfig struct {
	PtpModeDesired    PTPMode
	PtpModeDiscovered PTPMode
	Status            ConfigStatus
	DiscoveredSlavePtpConfig,
	DiscoveredSlavePtpConfigSecondary,
	DiscoveredMasterPtpConfig ptpDiscoveryRes
	ConfiguredMasterPresent bool
}
type ptpDiscoveryRes struct {
	Label  string
	Config ptpv1.PtpConfig
}

const BasePtp4lConfig = `[global]
ptp_dst_mac 01:1B:19:00:00:00
p2p_dst_mac 01:80:C2:00:00:0E
network_transport UDPv4
domainNumber 24
logging_level 7`

func (obj ptpDiscoveryRes) String() string {
	return fmt.Sprintf("[label=%s, config name=%s]", obj.Label, obj.Config.Name)
}
func (obj TestConfig) String() string {
	return fmt.Sprintf("PtpModeDesired=%s, PtpModeDiscovered=%s, Status=%s, DiscoveredSlavePtpConfig=%s, DiscoveredSlavePtpConfigSecondary=%s, DiscoveredMasterPtpConfig=%s", obj.PtpModeDesired, obj.PtpModeDiscovered, obj.Status, obj.DiscoveredSlavePtpConfig, obj.DiscoveredSlavePtpConfigSecondary, obj.DiscoveredMasterPtpConfig)
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
	case BoundaryClock:
		return BoundaryClockString
	case DualNICBoundaryClock:
		return DualNICBoundaryClockString
	case Discovery:
		return DiscoveryString
	case None:
		return NoneString
	default:
		return OrdinaryClockString
	}
}

func StringToMode(aString string) PTPMode {
	switch aString {
	case OrdinaryClockString:
		return OrdinaryClock
	case BoundaryClockString:
		return BoundaryClock
	case DualNICBoundaryClockString:
		return DualNICBoundaryClock
	case DiscoveryString, legacyDiscoveryString:
		return Discovery
	case NoneString:
		return OrdinaryClock
	default:
		return OrdinaryClock
	}
}

var GlobalConfig TestConfig

func init() {
	Reset()
}
func Reset() {
	GlobalConfig.PtpModeDesired = None
	GlobalConfig.PtpModeDiscovered = None
	GlobalConfig.Status = Start
}
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
	case OrdinaryClock, BoundaryClock, DualNICBoundaryClock, Discovery:
		logrus.Infof("%s mode detected", mode)
		GlobalConfig.PtpModeDesired = mode
		GlobalConfig.Status = InitStatus
		return GlobalConfig
	case None:
		logrus.Infof("No test mode specified using, %s mode. Specify the env variable PTP_TEST_MODE with one of %s, %s, %s, %s", OrdinaryClock, Discovery, OrdinaryClock, BoundaryClock, DualNICBoundaryClockString)
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

// Returns the slave node label to be used in the test
func discoverPTPConfiguration(namespace string) {
	var slaves []ptpv1.PtpConfig
	var masters []ptpv1.PtpConfig

	configList, err := testclient.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logrus.Errorf("error getting ptpconfig list, err=%s", err)
	}
	logrus.Infof("%d ptpconfig objects recovered", len(configList.Items))
	for _, config := range configList.Items {
		for _, profile := range config.Spec.Profile {
			if IsPtpMaster(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				masters = append(masters, config)
			}
			if IsPtpSlave(profile.Ptp4lOpts, profile.Phc2sysOpts) {
				slaves = append(slaves, config)
			}
		}
	}

	//GlobalConfig.DiscoveredMasterPtpConfig=checkPtpProfileLabels(masters)
	discoverMode(slaves, masters)
}

func discoverMode(slaves, masters []ptpv1.PtpConfig) {
	GlobalConfig.Status = DiscoveryFailureStatus
	if len(slaves) == 0 {
		logrus.Warnf("No Configs present, cannot discover")
		return
	}
	numBc := 0
	numSecondaryBC := 0
	pickedMasterConfig := checkPtpProfileLabels(masters)
	GlobalConfig.DiscoveredMasterPtpConfig = pickedMasterConfig
	for _, slave := range slaves {
		pickedSlaveConfig := checkSinglePtpProfileLabels(slave)
		if pickedSlaveConfig.Config.Name != "" {

			masterIf := len(ptpv1.GetInterfaces(pickedSlaveConfig.Config, ptpv1.Master))
			slaveIf := len(ptpv1.GetInterfaces(pickedSlaveConfig.Config, ptpv1.Slave))
			// OC
			if masterIf == 0 && slaveIf == 1 {
				GlobalConfig.PtpModeDiscovered = OrdinaryClock
				GlobalConfig.Status = DiscoverySuccessStatus
				GlobalConfig.DiscoveredSlavePtpConfig = pickedSlaveConfig
				break
			}
			// BC and Dual NIC BC
			if masterIf >= 1 && slaveIf >= 1 {
				if numBc == 0 {
					GlobalConfig.DiscoveredSlavePtpConfig = pickedSlaveConfig
				}
				if numBc == 1 {
					GlobalConfig.DiscoveredSlavePtpConfigSecondary = pickedSlaveConfig
				}
				numBc++
				if isSecondaryBc(pickedSlaveConfig) {
					numSecondaryBC++
				}
			}
		} else {
			GlobalConfig.Status = DiscoveryFailureStatus
		}
	}
	if numBc == 1 {
		GlobalConfig.PtpModeDiscovered = BoundaryClock
		GlobalConfig.Status = DiscoverySuccessStatus
	}
	if numBc == 2 && numSecondaryBC == 1 {
		GlobalConfig.PtpModeDiscovered = DualNICBoundaryClock
		GlobalConfig.Status = DiscoverySuccessStatus
	}

}

func isSecondaryBc(config ptpDiscoveryRes) bool {
	for _, profile := range config.Config.Spec.Profile {
		if profile.Phc2sysOpts != nil {
			return false
		}
	}
	return true
}

func IsPtpSlave(ptp4lOpts *string, phc2sysOpts *string) bool {
	return strings.Contains(*ptp4lOpts, "-s") &&
		((phc2sysOpts != nil && (strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 1)) ||
			phc2sysOpts == nil)

}

func IsPtpMaster(ptp4lOpts *string, phc2sysOpts *string) bool {
	return ptp4lOpts != nil && phc2sysOpts != nil && !strings.Contains(*ptp4lOpts, "-s ") && strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 2
}

func checkPtpProfileLabels(configs []ptpv1.PtpConfig) ptpDiscoveryRes {
	for _, config := range configs {
		return checkSinglePtpProfileLabels(config)
	}
	return ptpDiscoveryRes{"", ptpv1.PtpConfig{}}
}
func checkSinglePtpProfileLabels(config ptpv1.PtpConfig) ptpDiscoveryRes {
	for _, recommend := range config.Spec.Recommend {
		for _, match := range recommend.Match {
			label := *match.NodeLabel
			nodeCount := checkLabeledNodesExists(label)

			if nodeCount > 0 {
				return ptpDiscoveryRes{label, config}
			}
		}
	}
	return ptpDiscoveryRes{"", ptpv1.PtpConfig{}}
}
func checkLabeledNodesExists(label string) int {
	nodeList, err := testclient.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=", label)})
	if err != nil {
		logrus.Errorf("error listing nodes, err=%s", err)
		return 0
	}
	return len(nodeList.Items)
}
