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
	// OrdinaryClockString matches the OC clock mode in Environement
	OrdinaryClockString = "OC"
	// BoundaryClockString matches the BC clock mode in Environement
	BoundaryClockString = "BC"
	// DualNICBoundaryClockString matches the DualNICBC clock mode in Environement
	DualNICBoundaryClockString = "DualNICBC"
	ptp4lEthernet              = "-2"
	ptp4lEthernetSlave         = "-2 -s"
	phc2sysGM                  = "-a -r -r"
	phc2sysSlave               = "-a -r"
	SCHED_OTHER                = "SCHED_OTHER"
	SCHED_FIFO                 = "SCHED_FIFO"
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
	DiscoveredGrandMasterPtpConfig,
	DiscoveredSlave1PtpConfig,
	DiscoveredSlave2PtpConfig,
	DiscoveredClockUnderTestPtpConfig,
	DiscoveredClockUnderTestSecondaryPtpConfig *ptpDiscoveryRes
	L2Config *l2discovery.L2DiscoveryConfig
}
type ptpDiscoveryRes ptpv1.PtpConfig

const BasePtp4lConfig = `[global]
tx_timestamp_timeout 50
ptp_dst_mac 01:1B:19:00:00:00
p2p_dst_mac 01:80:C2:00:00:0E
domainNumber 24
logging_level 7`

func (obj *ptpDiscoveryRes) String() string {
	if obj == nil {
		return "nil"
	}
	return obj.Name
}
func (obj *TestConfig) String() string {
	if obj == nil {
		return "nil"
	}
	return fmt.Sprintf("PtpModeDesired=%s, PtpModeDiscovered=%s, Status=%s, DiscoveredClockUnderTestPtpConfig=%s, DiscoveredClockUnderTestSecondaryPtpConfig=%s, DiscoveredGrandMasterPtpConfig=%s, DiscoveredSlave1PtpConfig=%s, DiscoveredSlave2PtpConfig=%s",
		obj.PtpModeDesired,
		obj.PtpModeDiscovered,
		obj.Status,
		obj.DiscoveredClockUnderTestPtpConfig,
		obj.DiscoveredClockUnderTestSecondaryPtpConfig,
		obj.DiscoveredGrandMasterPtpConfig,
		obj.DiscoveredSlave1PtpConfig,
		obj.DiscoveredSlave2PtpConfig)
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
	switch strings.ToLower(aString) {
	case strings.ToLower(OrdinaryClockString):
		return OrdinaryClock
	case strings.ToLower(BoundaryClockString):
		return BoundaryClock
	case strings.ToLower(DualNICBoundaryClockString):
		return DualNICBoundaryClock
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

		if len(GlobalConfig.L2Config.Solutions) == 0 {
			logrus.Errorf("Could not find a solution")
			return
		}
		switch GlobalConfig.PtpModeDesired {
		case Discovery, None:
			logrus.Errorf("error creating ptpconfig Discovery, None not supported")
		case OrdinaryClock:
			PtpConfigOC(GlobalConfig.L2Config)

		case BoundaryClock:
			PtpConfigBC(GlobalConfig.L2Config)
		case DualNICBoundaryClock:
			PtpConfigDualNicBC(GlobalConfig.L2Config)
		}

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

func CreatePtpConfigGrandMaster(nodeName, ifName string) error {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	// Labeling the grandmaster node
	_, err = nodes.LabelNode(nodeName, utils.PtpGrandmasterNodeLabel, "")
	if err != nil {
		logrus.Errorf("Error setting Grandmaster node role label: %s", err)
	}

	// Grandmaster
	gmConfig := BasePtp4lConfig + "\nmasterOnly 1"
	ptp4lsysOpts := ptp4lEthernet
	phc2sysOpts := phc2sysGM
	return createConfig(utils.PtpGrandMasterPolicyName,
		&ifName,
		&ptp4lsysOpts,
		gmConfig,
		&phc2sysOpts,
		utils.PtpGrandmasterNodeLabel,
		pointer.Int64Ptr(int5),
		ptpSchedulingPolicy,
		pointer.Int64Ptr(int65))

}

func CreatePtpConfigBC(policyName, nodeName, ifMasterName, ifSlaveName string, phc2sys bool) (err error) {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
	_, err = nodes.LabelNode(nodeName, utils.PtpClockUnderTestNodeLabel, "")
	if err != nil {
		logrus.Errorf("Error setting BC node role label: %s", err)
	}

	bcConfig := BasePtp4lConfig + "\nboundary_clock_jbod 1\ngmCapable 0"
	bcConfig = AddInterface(bcConfig, ifSlaveName, 0)
	bcConfig = AddInterface(bcConfig, ifMasterName, 1)
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
		utils.PtpClockUnderTestNodeLabel,
		pointer.Int64Ptr(int5),
		ptpSchedulingPolicy,
		pointer.Int64Ptr(int65))

}

func CreatePtpConfigOC(profileName, nodeName, ifSlaveName string, phc2sys bool, label string) (err error) {
	ptpSchedulingPolicy := SCHED_OTHER
	configureFifo, err := strconv.ParseBool(os.Getenv("CONFIGURE_FIFO"))
	if err == nil && configureFifo {
		ptpSchedulingPolicy = SCHED_FIFO
	}
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

	return createConfig(profileName,
		&ifSlaveName,
		&ptp4lsysOpts,
		BasePtp4lConfig,
		phc2sysOpts,
		label,
		pointer.Int64Ptr(int5),
		ptpSchedulingPolicy,
		pointer.Int64Ptr(int65))
}

func PtpConfigOC(config *l2discovery.L2DiscoveryConfig) {
	var grandmaster, slave1 int

	BestSolution := l2discovery.AlgoOC

	if len(config.Solutions[l2discovery.AlgoOC]) == 0 &&
		len(config.Solutions[l2discovery.AlgoSNOOC]) == 0 {
		logrus.Infof("Could not configure OC clock")
		return
	}

	if len(config.Solutions[l2discovery.AlgoOC]) == 0 &&
		len(config.Solutions[l2discovery.AlgoSNOOC]) != 0 {
		BestSolution = l2discovery.AlgoSNOOC
	}

	switch BestSolution {
	case l2discovery.AlgoOC:
		grandmaster = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Grandmaster]
		slave1 = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Slave1]

		gmIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][grandmaster]]
		slave1If := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][slave1]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(utils.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, true, utils.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}

	case l2discovery.AlgoSNOOC:

		slave1 = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Slave1]

		slave1If := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][slave1]]

		err := CreatePtpConfigOC(utils.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, true, utils.PtpClockUnderTestNodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
	}

}
func PtpConfigBC(config *l2discovery.L2DiscoveryConfig) {
	var grandmaster, bc1Master, bc1Slave, slave1 int

	BestSolution := l2discovery.AlgoBCWithSlaves

	if len(config.Solutions[l2discovery.AlgoBCWithSlaves]) == 0 &&
		len(config.Solutions[l2discovery.AlgoBC]) == 0 &&
		len(config.Solutions[l2discovery.AlgoSNOBC]) == 0 {
		logrus.Infof("Could not configure BC clock")
		return
	}

	if len(config.Solutions[l2discovery.AlgoBCWithSlaves]) == 0 &&
		len(config.Solutions[l2discovery.AlgoBC]) != 0 {
		BestSolution = l2discovery.AlgoBC
	}

	if len(config.Solutions[l2discovery.AlgoBCWithSlaves]) == 0 &&
		len(config.Solutions[l2discovery.AlgoBC]) == 0 &&
		len(config.Solutions[l2discovery.AlgoSNOBC]) != 0 {
		BestSolution = l2discovery.AlgoSNOBC
	}

	switch BestSolution {
	case l2discovery.AlgoBCWithSlaves:
		grandmaster = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Grandmaster]
		bc1Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Master]
		bc1Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Slave]
		slave1 = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Slave1]

		gmIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][grandmaster]]
		bc1MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Master]]
		bc1SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Slave]]
		slave1If := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][slave1]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(utils.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, false, utils.PtpSlave1NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}

	case l2discovery.AlgoBC:
		grandmaster = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Grandmaster]
		bc1Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Master]
		bc1Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Slave]

		gmIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][grandmaster]]
		bc1MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Master]]
		bc1SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Slave]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

	case l2discovery.AlgoSNOBC:

		bc1Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Master]
		bc1Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Slave]

		bc1MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Master]]
		bc1SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Slave]]

		err := CreatePtpConfigBC(utils.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}
	}

}

func PtpConfigDualNicBC(config *l2discovery.L2DiscoveryConfig) {

	var grandmaster, bc1Master, bc1Slave, slave1, bc2Master, bc2Slave, slave2 int

	BestSolution := l2discovery.AlgoDualNicBCWithSlaves

	if len(config.Solutions[l2discovery.AlgoDualNicBCWithSlaves]) == 0 &&
		len(config.Solutions[l2discovery.AlgoDualNicBC]) == 0 &&
		len(config.Solutions[l2discovery.AlgoSNODualNicBC]) == 0 {
		logrus.Infof("Could not configure Dual NIC BC clock")
		return
	}

	if len(config.Solutions[l2discovery.AlgoDualNicBCWithSlaves]) == 0 &&
		len(config.Solutions[l2discovery.AlgoDualNicBC]) != 0 {
		BestSolution = l2discovery.AlgoDualNicBC
	}

	if len(config.Solutions[l2discovery.AlgoDualNicBCWithSlaves]) == 0 &&
		len(config.Solutions[l2discovery.AlgoDualNicBC]) == 0 &&
		len(config.Solutions[l2discovery.AlgoSNODualNicBC]) != 0 {
		BestSolution = l2discovery.AlgoSNODualNicBC
	}

	switch BestSolution {
	case l2discovery.AlgoDualNicBCWithSlaves:
		grandmaster = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Grandmaster]
		bc1Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Master]
		bc1Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Slave]
		slave1 = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Slave1]
		bc2Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC2Master]
		bc2Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC2Slave]
		slave2 = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Slave2]

		gmIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][grandmaster]]
		bc1MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Master]]
		bc1SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Slave]]
		slave1If := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][slave1]]
		bc2MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc2Master]]
		bc2SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc2Slave]]
		slave2If := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][slave2]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}

		err = CreatePtpConfigOC(utils.PtpSlave1PolicyName, slave1If.NodeName,
			slave1If.IfName, false, utils.PtpSlave1NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave1 ptpconfig: %s", err)
		}
		err = CreatePtpConfigOC(utils.PtpSlave2PolicyName, slave2If.NodeName,
			slave2If.IfName, false, utils.PtpSlave2NodeLabel)
		if err != nil {
			logrus.Errorf("Error creating Slave2 ptpconfig: %s", err)
		}

	case l2discovery.AlgoDualNicBC:
		grandmaster = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.Grandmaster]
		bc1Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Master]
		bc1Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Slave]
		bc2Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC2Master]
		bc2Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC2Slave]

		gmIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][grandmaster]]
		bc1MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Master]]
		bc1SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Slave]]
		bc2MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc2Master]]
		bc2SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc2Slave]]

		err := CreatePtpConfigGrandMaster(gmIf.NodeName,
			gmIf.IfName)
		if err != nil {
			logrus.Errorf("Error creating Grandmaster ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}

	case l2discovery.AlgoSNODualNicBC:

		bc1Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Master]
		bc1Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC1Slave]
		bc2Master = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC2Master]
		bc2Slave = config.TestClockRolesAlgoMapping[BestSolution][l2discovery.BC2Slave]

		bc1MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Master]]
		bc1SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc1Slave]]
		bc2MasterIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc2Master]]
		bc2SlaveIf := config.PtpIfList[config.Solutions[BestSolution][l2discovery.FirstSolution][bc2Slave]]

		err := CreatePtpConfigBC(utils.PtpBcMaster1PolicyName, bc1MasterIf.NodeName,
			bc1MasterIf.IfName, bc1SlaveIf.IfName, true)
		if err != nil {
			logrus.Errorf("Error creating bc1master ptpconfig: %s", err)
		}

		err = CreatePtpConfigBC(utils.PtpBcMaster2PolicyName, bc2MasterIf.NodeName,
			bc2MasterIf.IfName, bc2SlaveIf.IfName, false)
		if err != nil {
			logrus.Errorf("Error creating bc2master ptpconfig: %s", err)
		}
	}

}

// helper function to add an interface to the ptp4l config
func AddInterface(ptpConfig, iface string, masterOnly int) (updatedPtpConfig string) {
	return fmt.Sprintf("%s\n[%s]\nmasterOnly %d", ptpConfig, iface, masterOnly)
}

// helper function to create a ptpconfig
func createConfig(profileName string, ifaceName, ptp4lOpts *string, ptp4lConfig string, phc2sysOpts *string, nodeLabel string, priority *int64, ptpSchedulingPolicy string, ptpSchedulingPriority *int64) error {

	ptpProfile := ptpv1.PtpProfile{Name: &profileName, Interface: ifaceName, Phc2sysOpts: phc2sysOpts, Ptp4lOpts: ptp4lOpts, PtpSchedulingPolicy: &ptpSchedulingPolicy, PtpSchedulingPriority: ptpSchedulingPriority}
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
	var ptpConfigClockUnderTest []*ptpv1.PtpConfig

	configList, err := client.Client.PtpConfigs(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logrus.Errorf("error getting ptpconfig list, err=%s", err)
	}
	logrus.Infof("%d ptpconfig objects recovered", len(configList.Items))
	for profileIndex := range configList.Items {
		for _, r := range configList.Items[profileIndex].Spec.Recommend {
			for _, m := range r.Match {
				if m.NodeLabel == nil {
					continue
				}
				if *m.NodeLabel == utils.PtpClockUnderTestNodeLabel {
					ptpConfigClockUnderTest = append(ptpConfigClockUnderTest, &configList.Items[profileIndex])
				}

				// Grand master, slave 1 and slave 2 are checked as they are always created by the test program
				if GlobalConfig.PtpModeDesired != Discovery && GlobalConfig.PtpModeDesired != None {
					if *m.NodeLabel == utils.PtpGrandmasterNodeLabel {
						GlobalConfig.DiscoveredGrandMasterPtpConfig = (*ptpDiscoveryRes)(&configList.Items[profileIndex])
					}
					if *m.NodeLabel == utils.PtpSlave1NodeLabel {
						GlobalConfig.DiscoveredSlave1PtpConfig = (*ptpDiscoveryRes)(&configList.Items[profileIndex])
					}
					if *m.NodeLabel == utils.PtpSlave2NodeLabel {
						GlobalConfig.DiscoveredSlave2PtpConfig = (*ptpDiscoveryRes)(&configList.Items[profileIndex])
					}
				}
			}
		}
	}
	discoverMode(ptpConfigClockUnderTest)
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

	GlobalConfig.Status = DiscoveryFailureStatus

	for _, ptpConfig := range ptpConfigClockUnderTest {
		masterIf := len(ptpv1.GetInterfaces(*ptpConfig, ptpv1.Master))
		slaveIf := len(ptpv1.GetInterfaces(*ptpConfig, ptpv1.Slave))
		// OC
		if masterIf == 0 && slaveIf == 1 && len(ptpConfigClockUnderTest) == 1 {
			GlobalConfig.PtpModeDiscovered = OrdinaryClock
			GlobalConfig.Status = DiscoverySuccessStatus
			GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			break
		}
		// BC and Dual NIC BC
		if masterIf >= 1 && slaveIf >= 1 {
			if numBc == 0 {
				GlobalConfig.DiscoveredClockUnderTestPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			}
			if numBc == 1 {
				GlobalConfig.DiscoveredClockUnderTestSecondaryPtpConfig = (*ptpDiscoveryRes)(ptpConfig)
			}
			numBc++
			if isSecondaryBc(ptpConfig) {
				numSecondaryBC++
			}
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
func isSecondaryBc(config *ptpv1.PtpConfig) bool {
	for _, profile := range config.Spec.Profile {
		if profile.Phc2sysOpts != nil {
			return false
		}
	}
	return true
}

// Checks for OC
func IsPtpSlave(ptp4lOpts, phc2sysOpts *string) bool {
	return /*strings.Contains(*ptp4lOpts, "-s") &&*/ ((phc2sysOpts != nil && (strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 1)) ||
		phc2sysOpts == nil)
}

// Checks for Grand master
func IsPtpMaster(ptp4lOpts, phc2sysOpts *string) bool {
	return ptp4lOpts != nil && phc2sysOpts != nil && !strings.Contains(*ptp4lOpts, "-s ") && strings.Count(*phc2sysOpts, "-a") == 1 && strings.Count(*phc2sysOpts, "-r") == 2
}

// Checks for DualNIC BC
func GetProfileName(config *ptpv1.PtpConfig) (string, error) {
	for _, profile := range config.Spec.Profile {
		if profile.Name != nil && *profile.Name == utils.PtpGrandMasterPolicyName ||
			*profile.Name == utils.PtpBcMaster1PolicyName ||
			*profile.Name == utils.PtpBcMaster2PolicyName ||
			*profile.Name == utils.PtpSlave1PolicyName ||
			*profile.Name == utils.PtpSlave2PolicyName ||
			*profile.Name == utils.PtpTempPolicyName {
			return *profile.Name, nil
		}
	}
	return "", fmt.Errorf("cannot find valid test profile name")
}
