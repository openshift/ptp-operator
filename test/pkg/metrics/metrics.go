package metrics

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/pods"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	OpenshiftPtpInterfaceRole       = "openshift_ptp_interface_role"
	OpenshiftPtpClockState          = "openshift_ptp_clock_state"
	OpenshiftPtpFrequencyStatus     = "openshift_ptp_frequency_status"
	OpenshiftPtpPhaseStatus         = "openshift_ptp_phase_status"
	OpenshiftPtpOffsetNs            = "openshift_ptp_offset_ns"
	OpenshiftPtpProcessStatus       = "openshift_ptp_process_status"
	OpenshiftPtpClockClass          = "openshift_ptp_clock_class"
	OpenshiftPtpNMEAStatus          = "openshift_ptp_nmea_status"
	OpenshiftPtpThreshold           = "openshift_ptp_threshold"
	OpenshiftPtpProcessRestartCount = "openshift_ptp_process_restart_count"
	OpenshiftPtpHaProfileStatus     = "openshift_ptp_ha_profile_status"
	metricsEndPoint                 = "127.0.0.1:9091/metrics"
	MaxOffsetDefaultNs              = 100
	MinOffsetDefaultNs              = -100
	MaxInSpecOffsetDefaultNs        = 100
)

var MaxOffsetNs int
var MinOffsetNs int
var MaxInSpecOffsetNs int

// type and display for  OpenshiftPtpInterfaceRole metric. Values: 0 = PASSIVE, 1 = SLAVE, 2 = MASTER, 3 = FAULTY, 4 =  UNKNOWN
type MetricRole int

const (
	MetricRolePassive MetricRole = iota
	MetricRoleSlave
	MetricRoleMaster
	MetricRoleFaulty
	MetricRoleUnknown
	MetricRoleListening
)

const (
	MetricRolePassiveString   = "PASSIVE"
	MetricRoleSlaveString     = "SLAVE"
	MetricRoleMasterString    = "MASTER"
	MetricRoleFaultyString    = "FAULTY"
	MetricRoleUnknownString   = "UNKNOWN"
	MetricRoleListeningString = "LISTENING"
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
	case MetricRoleListening:
		return MetricRoleListeningString
	default:
		return ""
	}
}

// type and display for  OpenshiftPtpClockState metric. Values: 0 = FREERUN, 1 = LOCKED, 2 = HOLDOVER
type MetricClockState int

const (
	MetricClockStateFreeRun MetricClockState = iota
	MetricClockStateLocked
	MetricClockStateHoldOver
)

const (
	MetricClockStateFreeRunString  = "FREERUN"
	MetricClockStateLockedString   = "LOCKED"
	MetricClockStateHoldOverString = "HOLDOVER"
)

// Stringer for MetricClockState
func (role MetricClockState) String() string {
	switch role {
	case MetricClockStateFreeRun:
		return MetricClockStateFreeRunString
	case MetricClockStateLocked:
		return MetricClockStateLockedString
	case MetricClockStateHoldOver:
		return MetricClockStateHoldOverString
	default:
		return ""
	}
}

func GetPtpOffeset(aIf string, nodeName *string) (metric int, err error) {
	offsetString, err := getMetric(*nodeName, aIf, OpenshiftPtpOffsetNs)
	if err != nil {
		return 0, fmt.Errorf("error getting offset err:%s", err)
	}
	offsetInt, err := strconv.Atoi(offsetString)
	if err != nil {
		return 0, fmt.Errorf("error strconv for offsetString=%s, err:%s", offsetString, err)
	}

	return offsetInt, nil
}

func CheckClockState(state MetricClockState, aIf string, nodeName *string) (err error) {
	clockStateString, err := getMetric(*nodeName, aIf, OpenshiftPtpClockState)
	if err != nil {
		return fmt.Errorf("error getting clock state err:%s", err)
	}
	clockStateInt, err := strconv.Atoi(clockStateString)
	if err != nil {
		return fmt.Errorf("error strconv for clockStateString=%s, err:%s", clockStateString, err)
	}
	if MetricClockState(clockStateInt) != state {
		return fmt.Errorf("incorrect clock state")
	}
	return nil
}

// CheckClockRealTimeState verifies openshift_ptp_clock_state for
// iface=CLOCK_REALTIME, process=phc2sys on the given node.
func CheckClockRealTimeState(state MetricClockState, nodeName *string) error {
	if nodeName == nil || *nodeName == "" {
		return fmt.Errorf("nodeName is required")
	}
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return err
	}
	for index := range ptpPods.Items {
		if ptpPods.Items[index].Spec.NodeName != *nodeName {
			continue
		}
		buf, _, err := pods.ExecCommand(client.Client, false, &ptpPods.Items[index], pkg.PtpContainerName, []string{"curl", "-s", metricsEndPoint})
		if err != nil {
			return fmt.Errorf("error fetching metrics for CLOCK_REALTIME: %w", err)
		}
		metricsText := buf.String()
		for _, line := range strings.Split(metricsText, "\n") {
			if !strings.HasPrefix(line, OpenshiftPtpClockState+"{") {
				continue
			}
			if !strings.Contains(line, `iface="CLOCK_REALTIME"`) ||
				!strings.Contains(line, `process="phc2sys"`) ||
				!strings.Contains(line, fmt.Sprintf(`node="%s"`, *nodeName)) {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) != 2 {
				continue
			}
			clockStateInt, err := strconv.Atoi(parts[1])
			if err != nil {
				return fmt.Errorf("error strconv for CLOCK_REALTIME clock state %q: %w", parts[1], err)
			}
			if MetricClockState(clockStateInt) != state {
				return fmt.Errorf("CLOCK_REALTIME clock state expected=%d(%s) observed=%d(%s)",
					state, state.String(), clockStateInt, MetricClockState(clockStateInt).String())
			}
			return nil
		}
		return fmt.Errorf("openshift_ptp_clock_state iface=CLOCK_REALTIME process=phc2sys not found on node %s", *nodeName)
	}
	return fmt.Errorf("linuxptp-daemon pod not found on node %s", *nodeName)
}

// CheckHAProfileStatus verifies openshift_ptp_ha_profile_status for an HA member
// profile on a node
func CheckHAProfileStatus(profileRegEx *regexp.Regexp, active bool, nodeName *string) error {
	if nodeName == nil || *nodeName == "" {
		return fmt.Errorf("nodeName is required")
	}
	want := 0
	if active {
		want = 1
	}
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return err
	}
	for index := range ptpPods.Items {
		if ptpPods.Items[index].Spec.NodeName != *nodeName {
			continue
		}
		buf, _, err := pods.ExecCommand(client.Client, false, &ptpPods.Items[index], pkg.PtpContainerName, []string{"curl", "-s", metricsEndPoint})
		if err != nil {
			return fmt.Errorf("error fetching metrics for ha_profile_status: %w", err)
		}
		for _, line := range strings.Split(buf.String(), "\n") {
			if !strings.HasPrefix(line, OpenshiftPtpHaProfileStatus+"{") {
				continue
			}
			if !strings.Contains(line, `process="phc2sys"`) ||
				!profileRegEx.MatchString(line) ||
				!strings.Contains(line, fmt.Sprintf(`node="%s"`, *nodeName)) {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) != 2 {
				continue
			}
			value, err := strconv.Atoi(parts[1])
			if err != nil {
				return fmt.Errorf("error strconv for ha_profile_status %q: %w", parts[1], err)
			}
			if value != want {
				return fmt.Errorf("ha_profile_status for profile %q expected=%d observed=%d", profileRegEx.String(), want, value)
			}
			return nil
		}
		return fmt.Errorf("%s process=phc2sys profile=%q not found on node %s", OpenshiftPtpHaProfileStatus, profileRegEx.String(), *nodeName)
	}
	return fmt.Errorf("linuxptp-daemon pod not found on node %s", *nodeName)
}

// This method checks the state of the clock with specified interface
func CheckClockRole(roles []MetricRole, Ifs []string, nodeName *string) (err error) {
	if len(roles) != len(Ifs) {
		return fmt.Errorf("len(roles) != len(Ifs)")
	}
	for index := range Ifs {

		roleString, err := getMetric(*nodeName, Ifs[index], OpenshiftPtpInterfaceRole)
		if err != nil {
			return fmt.Errorf("error getting role err:%s", err)
		}
		roleInt, err := strconv.Atoi(roleString)
		if err != nil {
			return fmt.Errorf("error strconv for roleString=%s, err:%s", roleString, err)
		}
		if MetricRole(roleInt) != roles[index] {
			return fmt.Errorf("incorrect role for %s, role expected=%d(%s), role observed=%d(%s)", Ifs[index], roles[index], roles[index].String(), roleInt, MetricRole(roleInt).String())
		}
	}
	return nil
}

// This method checks the state of the clock with specified interface
func GetClockIfRoles(Ifs []string, nodeName *string) (roleInt []MetricRole, err error) {

	for index := range Ifs {

		roleString, err := getMetric(*nodeName, Ifs[index], OpenshiftPtpInterfaceRole)
		if err != nil {
			return roleInt, fmt.Errorf("error getting role err:%s", err)
		}
		var tempInt int
		tempInt, err = strconv.Atoi(roleString)
		if err != nil {
			return roleInt, fmt.Errorf("error strconv for roleString=%s, err:%s", roleString, err)
		}
		roleInt = append(roleInt, MetricRole(tempInt))
	}
	return roleInt, nil
}

// getIfaceAlias generates the PHC iface alias used in openshift_ptp_* metrics.
// Must stay aligned with linuxptp-daemon/cloud-event-proxy GetAlias:
//
//	ens2f0 -> ens2fx, ens2f0np0 -> ens2fx, ens1f0.100 -> ens1fx.100
func getIfaceAlias(ifname string) string {
	if ifname == "" {
		return ""
	}
	if alreadyAliasedPattern.MatchString(ifname) {
		return ifname
	}
	matches := ifaceAliasPattern.FindStringSubmatch(ifname)
	if len(matches) < 3 {
		return ifname
	}
	alias := matches[1] + "x"
	if len(matches) > 3 && matches[3] != "" {
		alias += matches[3]
	}
	return alias
}

var (
	alreadyAliasedPattern = regexp.MustCompile(`^(.+?)x(\..+)?$`)
	ifaceAliasPattern     = regexp.MustCompile(`^(.+?)(\d+)(?:np\d+)?(\..+)?$`)
)

// gets a metric value string for a given node and interface
func getMetric(nodeName, aIf, metricName string) (metric string, err error) {
	const (
		fromMaster = `from="master",`
	)
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
	if err != nil {
		return metric, err
	}
	var availableLines []string
	for index := range ptpPods.Items {
		if ptpPods.Items[index].Spec.NodeName != nodeName {
			continue
		}
		commands := []string{
			"curl", "-s", metricsEndPoint,
		}
		buf, _, err := pods.ExecCommand(client.Client, false, &ptpPods.Items[index], ptpPods.Items[index].Spec.Containers[0].Name, commands)
		if err != nil {
			return metric, fmt.Errorf("error getting ptp pods for metric: %s not found, err: %s", metricName, err)
		}

		metrics := buf.String()
		node := ptpPods.Items[index].Spec.NodeName

		// Build a list of interface names to try: first the original, then the PHC alias.
		// Cards with a PHC per port keep the original name; cards sharing a PHC use the masked alias
		// (e.g. ens2f0 -> ens2fx, ens2f0np0 -> ens2fx).
		ifCandidates := []string{aIf}
		aliasedIf := getIfaceAlias(aIf)
		if aliasedIf != "" && aliasedIf != aIf {
			ifCandidates = append(ifCandidates, aliasedIf)
		}

		for _, candidate := range ifCandidates {
			var regex string
			if metricName == OpenshiftPtpOffsetNs {
				regex = metricName + `{` + fromMaster + `iface="` + candidate + `",node="` + node + `",process="ptp4l"} (-*[0-9]*)`
			} else if metricName == OpenshiftPtpClockState {
				regex = metricName + `{iface="` + candidate + `",node="` + node + `",process="ptp4l"} (-*[0-9]*)`
			} else {
				regex = metricName + `{iface="` + candidate + `",node="` + node + `",process="ptp4l"} (-*[0-9]*)`
			}
			r := regexp.MustCompile(regex)
			for _, submatches := range r.FindAllStringSubmatchIndex(metrics, -1) {
				metric = string(r.ExpandString([]byte{}, "$1", metrics, submatches))
				return metric, nil
			}
		}

		// Metric not found — collect available lines for this metric name to include in error
		for _, line := range strings.Split(metrics, "\n") {
			if strings.HasPrefix(line, metricName+"{") {
				availableLines = append(availableLines, line)
			}
		}
		break
	}
	return metric, fmt.Errorf("metric: %s, nodeName: %s, aIf: %s not found, available %s metrics: %v",
		metricName, nodeName, aIf, metricName, availableLines)
}

// gets a node name based on a label
func getNode(label string) (nodeName string, err error) {
	ptpPods, err := client.Client.CoreV1().Pods(pkg.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: "app=linuxptp-daemon"})
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
func CheckClockRoleAndOffset(ptpConfig *ptpv1.PtpConfig, label, nodeName *string, expectedClockState MetricClockState, expectedClockRole MetricRole, isCheckOffset bool) (err error) {
	if nodeName == nil {
		var name string
		name, err = getNode(*label)
		if err != nil ||
			name == "" ||
			label == nil ||
			(*label != pkg.PtpClockUnderTestNodeLabel &&
				*label != pkg.PtpSlave1NodeLabel &&
				*label != pkg.PtpSlave2NodeLabel) {
			fmt.Printf(`error getting node name for label %s
Did you label the node running the clock under test with the %s label?
Only this label should be used to identify the clock under test. err:%s`, *label, pkg.PtpClockUnderTestNodeLabel, err)
			os.Exit(1)
		}
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
			return fmt.Errorf("error strconv for role=%s, err:%s", role, err)
		}
		logrus.Infof("nodeName=%s, aIf=%s, roleInt=%s", *nodeName, aIf, MetricRole(roleInt))

		if MetricRole(roleInt) != MetricRoleMaster {
			return fmt.Errorf("incorrect metric role: expecting %s found %s", MetricRoleMaster.String(), MetricRole(roleInt).String())
		}
	}
	// Find the port in SLAVE state and verify metrics
	var ifResults []string
	for _, aIf := range slaveIfs {

		// Check role
		roleString, err := getMetric(*nodeName, aIf, OpenshiftPtpInterfaceRole)
		if err != nil {
			logrus.Errorf("error getting role err:%s", err)
			ifResults = append(ifResults, fmt.Sprintf("  %s: role metric error: %s", aIf, err))
			continue
		}
		roleInt, err := strconv.Atoi(roleString)
		if err != nil {
			return fmt.Errorf("error strconv for roleString=%s, err:%s", roleString, err)
		}
		logrus.Infof("nodeName=%s, aIf=%s, roleInt=%s", *nodeName, aIf, MetricRole(roleInt))
		if MetricRole(roleInt) != expectedClockRole {
			logrus.Errorf("incorrect role, continue looking for other interfaces")
			ifResults = append(ifResults, fmt.Sprintf("  %s: role mismatch (expected=%s, got=%s)", aIf, expectedClockRole, MetricRole(roleInt)))
			continue
		}

		// Check ptp clock state
		clockStateString, err := getMetric(*nodeName, aIf, OpenshiftPtpClockState)
		if err != nil {
			logrus.Errorf("error getting clock state err:%s", err)
			ifResults = append(ifResults, fmt.Sprintf("  %s: clock state metric error: %s", aIf, err))
			continue
		}
		clockStateInt, err := strconv.Atoi(clockStateString)
		if err != nil {
			ifResults = append(ifResults, fmt.Sprintf("  %s: clock state strconv error: %s", aIf, err))
			return fmt.Errorf("error strconv for clockStateString=%s, err:%s. Per-interface results:\n%s", clockStateString, err, strings.Join(ifResults, "\n"))
		}
		logrus.Infof("nodeName=%s, aIf=%s, clockStateInt=%s expectedClockstate=%s", *nodeName, aIf, MetricClockState(clockStateInt), expectedClockState)
		if MetricClockState(clockStateInt) != expectedClockState {
			ifResults = append(ifResults, fmt.Sprintf("  %s: clock state mismatch (expected=%s, got=%s)", aIf, expectedClockState, MetricClockState(clockStateInt)))
			return fmt.Errorf("incorrect clock state on node %s iface %s: expected %s, got %s. Per-interface results:\n%s",
				*nodeName, aIf, expectedClockState, MetricClockState(clockStateInt), strings.Join(ifResults, "\n"))
		}

		// Check offset
		if !isCheckOffset {
			return nil
		}
		offsetString, err := getMetric(*nodeName, aIf, OpenshiftPtpOffsetNs)
		if err != nil {
			return fmt.Errorf("error getting offset err:%s", err)
		}
		offsetInt, err := strconv.Atoi(offsetString)
		if err != nil {
			return fmt.Errorf("error strconv for offsetString=%s, err:%s", offsetString, err)
		}
		if offsetInt > MaxOffsetNs || offsetInt < MinOffsetNs {
			return fmt.Errorf("incorrect offset %d ns > %d ns", offsetInt, MaxOffsetNs)
		}
		logrus.Infof("Clock sync offset withing expected range min=%d ns < %d ns < max=%d ns", MinOffsetNs, offsetInt, MaxOffsetNs)
		return nil
	}
	return fmt.Errorf("no Follower port in expected %s state for ptpconfig %s (node=%s, label=%q, slaveIfs=%v). Per-interface results:\n%s",
		expectedClockState, ptpConfig.Name, *nodeName, pkg.PtrStringOrDefault(label, "<nil>"), slaveIfs, strings.Join(ifResults, "\n"))
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
