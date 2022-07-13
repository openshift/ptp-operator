package utils

const (
	// NamespaceTesting contains the name of the testing namespace

	ETHTOOL_HARDWARE_RECEIVE_CAP    = "hardware-receive"
	ETHTOOL_HARDWARE_TRANSMIT_CAP   = "hardware-transmit"
	ETHTOOL_HARDWARE_RAW_CLOCK_CAP  = "hardware-raw-clock"
	ETHTOOL_RX_HARDWARE_FLAG        = "(SOF_TIMESTAMPING_RX_HARDWARE)"
	ETHTOOL_TX_HARDWARE_FLAG        = "(SOF_TIMESTAMPING_TX_HARDWARE)"
	ETHTOOL_RAW_HARDWARE_FLAG       = "(SOF_TIMESTAMPING_RAW_HARDWARE)"
	PtpLinuxDaemonNamespace         = "openshift-ptp"
	PtpOperatorDeploymentName       = "ptp-operator"
	PtpDaemonsetName                = "linuxptp-daemon"
	PtpGrandmasterNodeLabel         = "ptp/test-grandmaster"
	PtpSlaveNodeLabel               = "ptp/test-slave"
	PtpBCMasterNodeLabel            = "ptp/test-bc-master"
	PtpBCSlaveNodeLabel             = "ptp/test-bc-slave"
	PtpResourcesGroupVersionPrefix  = "ptp.openshift.io/v"
	PtpResourcesNameOperatorConfigs = "ptpoperatorconfigs"
	NodePtpDeviceAPIPath            = "/apis/ptp.openshift.io/v1/namespaces/openshift-ptp/nodeptpdevices/"
	ConfigPtpAPIPath                = "/apis/ptp.openshift.io/v1/namespaces/openshift-ptp/ptpconfigs"
	PtpContainerName                = "linuxptp-daemon-container"
	EventProxyContainerName         = "cloud-event-proxy"
	PtpGrandMasterPolicyName        = "test-grandmaster"
	PtpSlavePolicyName              = "test-slave"
	PtpBcMasterPolicyName           = "test-bc-master"
	PtpBcSlavePolicyName            = "test-bc-slave"
)

const (
	// PtpNamespace contains the name of the ptp namespace
	PtpNamespace = "openshift-ptp"
	// NodePtpDevices contains the name of the node ptp devices CRD
	NodePtpDevicesCRD = "nodeptpdevices.ptp.openshift.io"
	// PtpConfigs contains the name of the ptp configs CRD
	PtpConfigsCRD = "ptpconfigs.ptp.openshift.io"
	// PtpOperatorConfigs contains the name of the ptp operator config CRD
	PtpOperatorConfigsCRD = "ptpoperatorconfigs.ptp.openshift.io"
)
