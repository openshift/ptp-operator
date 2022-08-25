package metrics

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/pods"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	OpenshiftPtpInterfaceRole = "openshift_ptp_interface_role"
	OpenshiftPtpOffsetNs      = "openshift_ptp_offset_ns"
	OpenshiftPtpThreshold     = "openshift_ptp_threshold"
	metricsEndPoint           = "127.0.0.1:9091/metrics"
	MaxOffsetDefaultNs        = 100
	MinOffsetDefaultNs        = -100
)

var MaxOffsetNs int
var MinOffsetNs int

// type and display for  OpenshiftPtpInterfaceRole metric. Values: 0 = PASSIVE, 1 = SLAVE, 2 = MASTER, 3 = FAULTY, 4 =  UNKNOWN
type MetricRole int

const (
	MetricRolePassive MetricRole = iota
	MetricRoleSlave
	MetricRoleMaster
	MetricRoleFaulty
	MetricRoleUnknown
)

const (
	MetricRolePassiveString = "PASSIVE"
	MetricRoleSlaveString   = "SLAVE"
	MetricRoleMasterString  = "MASTER"
	MetricRoleFaultyString  = "FAULTY"
	MetricRoleUnknownString = "UNKNOWN"
)

// Stringer for MetricRole
func (role MetricRole) String() string {
	switch role {
	case MetricRolePassive:
		return MetricRolePassiveString
	case MetricRoleSlave:
		return MetricRoleSlaveString
	case MetricRoleMaster:
		return MetricRoleMasterString
	case MetricRoleFaulty:
		return MetricRoleFaultyString
	case MetricRoleUnknown:
		return MetricRoleUnknownString
	default:
		return ""
	}
}

// gets a metric value string for a given node and interface
func getMetric(nodeName, aIf, metricName string) (metric string, err error) {
	const (
		fromMaster = `from="master",`
	)
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return metric, err
	}
	for index := range ptpPods.Items {
		if ptpPods.Items[index].Spec.NodeName != nodeName {
			continue
		}
		commands := []string{
			"curl", "-s", metricsEndPoint,
		}
		buf, err := pods.ExecCommand(client.Client, &ptpPods.Items[index], ptpPods.Items[index].Spec.Containers[0].Name, commands)
		if err != nil {
			return metric, fmt.Errorf("error getting ptp pods for metric: %s not found, err: %s", metricName, err)
		}

		metrics := buf.String()
		var regex string
		if metricName == OpenshiftPtpOffsetNs {
			aIf = aIf[:len(aIf)-1] + "x"
			regex = metricName + `{` + fromMaster + `iface="` + aIf + `",node="` + ptpPods.Items[index].Spec.NodeName + `",process="ptp4l"} ([0-9]*)`
		} else {
			regex = metricName + `{iface="` + aIf + `",node="` + ptpPods.Items[index].Spec.NodeName + `",process="ptp4l"} ([0-9]*)`
		}
		r := regexp.MustCompile(regex)
		for _, submatches := range r.FindAllStringSubmatchIndex(metrics, -1) {
			metric = string(r.ExpandString([]byte{}, "$1", metrics, submatches))
			return metric, nil
		}
		break
	}
	return metric, fmt.Errorf("metric: %s not found", metricName)
}

// gets a node name based on a label
func getNode(label string) (nodeName string, err error) {
	ptpPods, err := client.Client.CoreV1().Pods(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return nodeName, err
	}
	for index := range ptpPods.Items {

		role, err := pods.PodRole(&ptpPods.Items[index], label)
		if err != nil {
			logrus.Errorf("cannot check pod role with err:%s", err)
		}
		if !role {
			continue
		}
		return ptpPods.Items[index].Spec.NodeName, nil
	}
	return nodeName, fmt.Errorf("node not found")
}

// Checks the accuracy of the clock defined by the ptpconfig passsed as a parameter:
// - checks the ptp offset to be less than MaxOffsetDefaultNs or any value passed by the user
// - check that the role of each interfaces in the ptpconfig matches the metric
func CheckClockRoleAndOffset(ptpConfig *ptpv1.PtpConfig, label, nodeName *string) (err error) {
	if nodeName == nil {
		var name string
		name, err = getNode(*label)
		nodeName = &name
	}
	masterIfs := ptpv1.GetInterfaces(*ptpConfig, ptpv1.Master)
	slaveIfs := ptpv1.GetInterfaces(*ptpConfig, ptpv1.Slave)

	for _, aIf := range masterIfs {
		role, err := getMetric(*nodeName, aIf, OpenshiftPtpInterfaceRole)
		if err != nil {
			return fmt.Errorf("error getting metric err:%s", err)
		}
		roleInt, err := strconv.Atoi(role)
		if err != nil {
			return fmt.Errorf("error strconv err:%s", err)
		}
		logrus.Infof("nodeName=%s, aIf=%s, roleInt=%s", *nodeName, aIf, MetricRole(roleInt))

		if MetricRole(roleInt) != MetricRoleMaster {
			return fmt.Errorf("incorrect role")
		}
	}
	for _, aIf := range slaveIfs {
		roleString, err := getMetric(*nodeName, aIf, OpenshiftPtpInterfaceRole)
		if err != nil {
			return fmt.Errorf("error getting role err:%s", err)
		}
		offsetString, err := getMetric(*nodeName, aIf, OpenshiftPtpOffsetNs)
		if err != nil {
			return fmt.Errorf("error getting offset err:%s", err)
		}
		roleInt, err := strconv.Atoi(roleString)
		if err != nil {
			return fmt.Errorf("error strconv err:%s", err)
		}
		offsetInt, err := strconv.Atoi(offsetString)
		if err != nil {
			return fmt.Errorf("error strconv err:%s", err)
		}
		logrus.Infof("nodeName=%s, aIf=%s, offsetInt=%d ns, roleInt=%s", *nodeName, aIf, offsetInt, MetricRole(roleInt))
		if MetricRole(roleInt) != MetricRoleSlave {
			return fmt.Errorf("incorrect role")
		}
		if offsetInt > MaxOffsetNs || offsetInt < MinOffsetNs {
			return fmt.Errorf("incorrect offset %d > %d", offsetInt, MaxOffsetNs)
		}
	}
	return nil
}

// Gets the first label configured in the ptpconfig->spec->recommend
func GetLabel(ptpConfig *ptpv1.PtpConfig) (*string, error) {
	for _, r := range ptpConfig.Spec.Recommend {
		for _, m := range r.Match {
			if m.NodeLabel == nil {
				continue
			}
			aLabel := ""
			switch *m.NodeLabel {
			case utils.PtpClockUnderTestNodeLabel:
				aLabel = utils.PtpClockUnderTestNodeLabel
			case utils.PtpGrandmasterNodeLabel:
				aLabel = utils.PtpGrandmasterNodeLabel
			case utils.PtpSlave1NodeLabel:
				aLabel = utils.PtpSlave1NodeLabel
			case utils.PtpSlave2NodeLabel:
				aLabel = utils.PtpSlave2NodeLabel
			}
			return &aLabel, nil
		}
	}
	return nil, fmt.Errorf("label not found")
}

// gets the first nodename configured in the ptpconfig->spec->recommend
func GetFirstNode(ptpConfig *ptpv1.PtpConfig) (*string, error) {
	for _, r := range ptpConfig.Spec.Recommend {
		for _, m := range r.Match {
			if m.NodeName == nil {
				continue
			}
			return m.NodeName, nil

		}
	}
	return nil, fmt.Errorf("nodeName not found")
}

// gets the user configured maximum offset in nanoseconds
func InitEnvIntParamConfig(envString string, defaultInt int, param *int) error {
	value, isSet := os.LookupEnv(envString)
	if !isSet {
		*param = defaultInt
		logrus.Infof("%s not set, assuming %d ns", envString, *param)
		return nil
	}
	value = strings.ToLower(value)
	var temp int
	temp, err := strconv.Atoi(value)
	*param = temp
	if err != nil {
		return fmt.Errorf("cannot parse %s, got %s, err:%s", envString, value, err)
	}

	logrus.Infof("%s=%d", envString, *param)
	return nil
}
