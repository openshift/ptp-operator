package testconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"context"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/clean"
	"github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/l2discovery"
	"github.com/openshift/ptp-operator/test/utils/nodes"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
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
	PtpLinuxDaemonNamespace      = "openshift-ptp"
	int65                        = 65
	int5                         = 5
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
	// None initial empty mode
	None
)

type TestConfig struct {
	PtpModeDesired    PTPMode
	PtpModeDiscovered PTPMode
	Status            ConfigStatus
	DiscoveredSlavePtpConfig,
	DiscoveredSlavePtpConfigSecondary,
	DiscoveredMasterPtpConfig *ptpDiscoveryRes
	L2Config                *l2discovery.L2DiscoveryConfig
	ConfiguredMasterPresent bool
}
type ptpDiscoveryRes struct {
	Label  string
	Config ptpv1.PtpConfig
}

const BasePtp4lConfig = `[global]
ptp_dst_mac 01:1B:19:00:00:00
p2p_dst_mac 01:80:C2:00:00:0E
domainNumber 24
logging_level 7`

func (obj *ptpDiscoveryRes) String() string {
	return fmt.Sprintf("[label=%s, config name=%s]", obj.Label, obj.Config.Name)
}
func (obj *TestConfig) String() string {
	return fmt.Sprintf("PtpModeDesired=%s, PtpModeDiscovered=%s, Status=%s, DiscoveredSlavePtpConfig=%s, DiscoveredSlavePtpConfigSecondary=%s, DiscoveredMasterPtpConfig=%s",
		obj.PtpModeDesired,
		obj.PtpModeDiscovered,
		obj.Status,
		obj.DiscoveredSlavePtpConfig,
		obj.DiscoveredSlavePtpConfigSecondary,
		obj.DiscoveredMasterPtpConfig)
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

// resets the test configuration
func Reset() {
	GlobalConfig.PtpModeDesired = None
	GlobalConfig.PtpModeDiscovered = None
	GlobalConfig.Status = Start
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

// Create ptpconfigs
func CreatePtpConfigurations() {
	var err error
	// Initialize desired ptp config for all configs
	GetDesiredConfig(true)
	// in multi node configuration create ptp configs
	if GlobalConfig.PtpModeDesired != Discovery {
		GlobalConfig.L2Config, err = l2discovery.GetL2DiscoveryConfig()
		if err != nil {
			logrus.Errorf("Error getting L2 discovery data, err=%s", err)
		}
		err = clean.All()
		if err != nil {
			logrus.Errorf("Error deleting labels and configuration, err=%s", err)
		}
		CreatePtpConfigs(GlobalConfig.L2Config)
	}
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

// Creates the ptpconfigs based on the calculated best clock configuration
func CreatePtpConfigs(config *l2discovery.L2DiscoveryConfig) {
	if config == nil {
		return
	}
	ptpSchedulingPolicy := "SCHED_OTHER"
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = "SCHED_FIFO"
	}
	if config.BestClockConfig.Grandmaster != nil {
		// Labeling the grandmaster node
		_, err = nodes.LabelNode(config.BestClockConfig.Grandmaster.NodeName, utils.PtpGrandmasterNodeLabel, "")
		if err != nil {
			logrus.Errorf("Error setting Grandmaster node role label: %s", err)
		}
	}
	if config.BestClockConfig.Slave != nil {
		// Labeling the Slave node
		_, err = nodes.LabelNode(config.BestClockConfig.Slave.NodeName, utils.PtpSlaveNodeLabel, "")
		if err != nil {
			logrus.Errorf("Error setting Slave node role label: %s", err)
		}
	}
	if len(config.BestClockConfig.BcMaster) > 0 &&
		config.BestClockConfig.BcMaster[0] != nil {
		// Labeling the BC master node
		_, err = nodes.LabelNode(config.BestClockConfig.BcMaster[0].NodeName, utils.PtpBCMasterNodeLabel, "")
		if err != nil {
			logrus.Errorf("Error setting BC Master node role label: %s", err)
		}
	}
	if len(config.BestClockConfig.BcSlave) > 0 &&
		config.BestClockConfig.BcSlave[0] != nil {
		// Labeling the BC Slave node
		_, err = nodes.LabelNode(config.BestClockConfig.BcSlave[0].NodeName, utils.PtpBCSlaveNodeLabel, "")
		if err != nil {
			logrus.Errorf("Error setting BC Slave node role label: %s", err)
		}
	}
	// Grandmaster
	if config.BestClockConfig.Grandmaster != nil {
		err = createConfig(utils.PtpGrandMasterPolicyName,
			&config.BestClockConfig.Grandmaster.IfName,
			"-2",
			BasePtp4lConfig,
			"-a -r -r",
			utils.PtpGrandmasterNodeLabel,
			pointer.Int64Ptr(int5),
			ptpSchedulingPolicy,
			pointer.Int64Ptr(int65))
	}
	switch GlobalConfig.PtpModeDesired {
	case Discovery, DualNICBoundaryClock, None:
		logrus.Errorf("error creating ptpconfig Discovery, DualNICBoundaryClock, None not supported")
	case OrdinaryClock:
		if config.BestClockConfig.Slave != nil {
			// Slave
			err = createConfig(utils.PtpSlavePolicyName,
				&config.BestClockConfig.Slave.IfName,
				"-s -2",
				BasePtp4lConfig,
				"-a -r",
				utils.PtpSlaveNodeLabel,
				pointer.Int64Ptr(int5),
				ptpSchedulingPolicy,
				pointer.Int64Ptr(int65))
			if err != nil {
				logrus.Errorf("error creating OC ptpconfig, err=%s", err)
			}
		}
	case BoundaryClock:
		if len(config.BestClockConfig.BcMaster) > 0 &&
			config.BestClockConfig.BcMaster[0] != nil {
			bcConfig := BasePtp4lConfig + "\nboundary_clock_jbod 1"
			bcConfig = AddInterface(bcConfig, config.BestClockConfig.Slave.IfName, 0)
			bcConfig = AddInterface(bcConfig, config.BestClockConfig.BcMaster[0].IfName, 1)
			err = createConfig(utils.PtpBcMasterPolicyName,
				nil,
				"-s -2",
				bcConfig,
				"-a -r",
				utils.PtpBCMasterNodeLabel,
				pointer.Int64Ptr(int5),
				ptpSchedulingPolicy,
				pointer.Int64Ptr(int65))
		}
		if err != nil {
			logrus.Errorf("error creating BC Master ptpconfig, err=%s", err)
		}
		if len(config.BestClockConfig.BcSlave) > 0 &&
			config.BestClockConfig.BcSlave[0] != nil {
			err = createConfig(utils.PtpBcSlavePolicyName,
				&config.BestClockConfig.BcSlave[0].IfName,
				`"-2"`,
				BasePtp4lConfig,
				"-a -r",
				utils.PtpBCSlaveNodeLabel,
				pointer.Int64Ptr(int5),
				ptpSchedulingPolicy,
				pointer.Int64Ptr(int65))
		}
		if err != nil {
			logrus.Errorf("error creating BC Slave ptpconfig, err=%s", err)
		}
	}
}

// helper function to add an interface to the ptp4l config
func AddInterface(ptpConfig, iface string, masterOnly int) (updatedPtpConfig string) {
	return fmt.Sprintf("%s\n[%s]\nmasterOnly %d", ptpConfig, iface, masterOnly)
}

// helper function to create a ptpconfig
func createConfig(profileName string, ifaceName *string, ptp4lOpts, ptp4lConfig, phc2sysOpts, nodeLabel string, priority *int64, ptpSchedulingPolicy string, ptpSchedulingPriority *int64) error {
	ptpProfile := ptpv1.PtpProfile{Name: &profileName, Interface: ifaceName, Phc2sysOpts: &phc2sysOpts, Ptp4lOpts: &ptp4lOpts, PtpSchedulingPolicy: &ptpSchedulingPolicy, PtpSchedulingPriority: ptpSchedulingPriority}
	if ptp4lConfig != "" {
		ptpProfile.Ptp4lConf = &ptp4lConfig
	}
	matchRule := ptpv1.MatchRule{NodeLabel: &nodeLabel}
	ptpRecommend := ptpv1.PtpRecommend{Profile: &profileName, Priority: priority, Match: []ptpv1.MatchRule{matchRule}}
	policy := ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: profileName, Namespace: PtpLinuxDaemonNamespace},
		Spec: ptpv1.PtpConfigSpec{Profile: []ptpv1.PtpProfile{ptpProfile}, Recommend: []ptpv1.PtpRecommend{ptpRecommend}}}

	_, err := client.Client.PtpConfigs(PtpLinuxDaemonNamespace).Create(context.Background(), &policy, metav1.CreateOptions{})
	return err
}

// Discovers the PTP configuration
func discoverPTPConfiguration(namespace string) {
	var slaves []*ptpv1.PtpConfig
	var masters []*ptpv1.PtpConfig

	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logrus.Errorf("error getting ptpconfig list, err=%s", err)
	}
	logrus.Infof("%d ptpconfig objects recovered", len(configList.Items))
	for profileIndex := range configList.Items {
		for _, profile := range configList.Items[profileIndex].Spec.Profile {
			if profile.Name != nil && *profile.Name != utils.PtpBcSlavePolicyName {
				if IsPtpMaster(profile.Ptp4lOpts, profile.Phc2sysOpts) {
					masters = append(masters, &configList.Items[profileIndex])
				}
				if IsPtpSlave(profile.Ptp4lOpts, profile.Phc2sysOpts) {
					slaves = append(slaves, &configList.Items[profileIndex])
				}
			}
		}
	}
	discoverMode(slaves, masters)
}

// Helper function analysing ptpconfig to deduce the actual ptp configuration
func discoverMode(slaves, masters []*ptpv1.PtpConfig) {
	GlobalConfig.Status = DiscoveryFailureStatus
	if len(slaves) == 0 {
		logrus.Warnf("No Configs present, cannot discover")
		return
	}
	numBc := 0
	numSecondaryBC := 0
	pickedMasterConfig := checkPtpProfileLabels(masters)
	GlobalConfig.DiscoveredMasterPtpConfig = &pickedMasterConfig
	for _, slave := range slaves {
		pickedSlaveConfig := checkSinglePtpProfileLabels(slave)
		if pickedSlaveConfig.Config.Name != "" {

			masterIf := len(ptpv1.GetInterfaces(pickedSlaveConfig.Config, ptpv1.Master))
			slaveIf := len(ptpv1.GetInterfaces(pickedSlaveConfig.Config, ptpv1.Slave))
			// OC
			if masterIf == 0 && slaveIf == 1 {
				GlobalConfig.PtpModeDiscovered = OrdinaryClock
				GlobalConfig.Status = DiscoverySuccessStatus
				GlobalConfig.DiscoveredSlavePtpConfig = &pickedSlaveConfig
				break
			}
			// BC and Dual NIC BC
			if masterIf >= 1 && slaveIf >= 1 {
				if numBc == 0 {
					GlobalConfig.DiscoveredSlavePtpConfig = &pickedSlaveConfig
				}
				if numBc == 1 {
					GlobalConfig.DiscoveredSlavePtpConfigSecondary = &pickedSlaveConfig
				}
				numBc++
				if isSecondaryBc(&pickedSlaveConfig) {
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

// Checks for DualNIC BC
func isSecondaryBc(config *ptpDiscoveryRes) bool {
	for _, profile := range config.Config.Spec.Profile {
		if profile.Phc2sysOpts != nil {
			return false
		}
	}
	return true
}

// Checks for OC
func IsPtpSlave(ptp4lOpts, phc2sysOpts *string) bool {
	return strings.Contains(*ptp4lOpts, "-s") &&
		((phc2sysOpts != nil && (strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 1)) ||
			phc2sysOpts == nil)
}

// Checks for Grand master
func IsPtpMaster(ptp4lOpts, phc2sysOpts *string) bool {
	return ptp4lOpts != nil && phc2sysOpts != nil && !strings.Contains(*ptp4lOpts, "-s ") && strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 2
}

// Checks for the presence of the ptp test node labels (GM, OC, BC, BC slave) from the profile in the node
func checkPtpProfileLabels(configs []*ptpv1.PtpConfig) ptpDiscoveryRes {
	for _, config := range configs {
		return checkSinglePtpProfileLabels(config)
	}
	return ptpDiscoveryRes{"", ptpv1.PtpConfig{}}
}

// Checks a single node
func checkSinglePtpProfileLabels(config *ptpv1.PtpConfig) ptpDiscoveryRes {
	for _, recommend := range config.Spec.Recommend {
		for _, match := range recommend.Match {
			label := *match.NodeLabel
			nodeCount := checkLabeledNodesExists(label)

			if nodeCount > 0 {
				return ptpDiscoveryRes{label, *config}
			}
		}
	}
	return ptpDiscoveryRes{"", ptpv1.PtpConfig{}}
}

// Checks for the presence of the ptp test node labels (GM, OC, BC, BC slave) from the profile in the node
func checkLabeledNodesExists(label string) int {
	nodeList, err := client.Client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=", label)})
	if err != nil {
		logrus.Errorf("error listing nodes, err=%s", err)
		return 0
	}
	return len(nodeList.Items)
}
