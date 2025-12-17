package l2lib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/redhat-cne/l2discovery-lib/exports"
	"github.com/redhat-cne/l2discovery-lib/pkg/l2client"
	"github.com/redhat-cne/l2discovery-lib/pkg/pods"
	daemonsets "github.com/redhat-cne/privileged-daemonset"
	"github.com/sirupsen/logrus"
	"github.com/yourbasic/graph"
	v1core "k8s.io/api/core/v1"
)

func init() {
	GlobalL2DiscoveryConfig.refresh = true
}

type L2Info interface {
	// list of cluster interfaces indexed with a simple integer (X) for readability in the graph
	GetPtpIfList() []*exports.PtpIf
	// list of unfiltered cluster interfaces indexed with a simple integer (X) for readability in the graph
	GetPtpIfListUnfiltered() map[string]*exports.PtpIf
	// LANs identified in the graph
	GetLANs() *[][]int
	// List of port receiving PTP frames (assuming valid GM signal received)
	GetPortsGettingPTP() []*exports.PtpIf

	SetL2Client(kubernetes.Interface, *rest.Config)
	GetL2DiscoveryConfig(ptpInterfacesOnly, allIFs, useContainerCmds bool, l2DiscoveryImage string) (config L2Info, err error)
}

const (
	ExperimentalEthertype          = "88b5"
	PtpEthertype                   = "88f7"
	LocalInterfaces                = "0000"
	L2DaemonsetManagedString       = "MANAGED"
	L2DaemonsetPreConfiguredString = "PRECONFIGURED"
	L2DiscoveryDsName              = "l2discovery"
	L2DiscoveryNsName              = "l2discovery"
	L2DiscoveryContainerName       = "l2discovery"
	timeoutDaemon                  = time.Second * 60
	L2DiscoveryDuration            = time.Second * 15
	L2ContainerCPULim              = "100m"
	L2ContainerCPUReq              = "100m"
	L2ContainerMemLim              = "100M"
	L2ContainerMemReq              = "100M"
	// ptpIfIndent defines the indentation level for PtpIf detailed output
	ptpIfIndent = 6
)

type L2DaemonsetMode int64

const (
	// In managed mode, the L2 Topology discovery Daemonset is created by the conformance suite
	Managed L2DaemonsetMode = iota
	// In pre-configured mode, the L2 topology daemonset is pre-configured by the user in the cluster
	PreConfigured
)

func (mode L2DaemonsetMode) String() string {
	switch mode {
	case Managed:
		return L2DaemonsetManagedString
	case PreConfigured:
		return L2DaemonsetPreConfiguredString
	default:
		return L2DaemonsetManagedString
	}
}

func StringToL2Mode(aString string) L2DaemonsetMode {
	switch aString {
	case L2DaemonsetManagedString:
		return Managed
	case L2DaemonsetPreConfiguredString:
		return PreConfigured
	default:
		return Managed
	}
}

type L2DiscoveryConfig struct {
	// Map of L2 topology as discovered by L2 discovery mechanism
	DiscoveryMap map[string]map[string]map[string]*exports.Neighbors
	// L2 topology graph created from discovery map. This is the main internal graph
	L2ConnectivityMap *graph.Mutable
	// Max size of graph
	MaxL2GraphSize int
	// list of cluster interfaces indexed with a simple integer (X) for readability in the graph
	PtpIfList []*exports.PtpIf
	// list of unfiltered cluster interfaces indexed with a simple integer (X) for readability in the graph
	PtpIfListUnfiltered map[string]*exports.PtpIf
	// list of L2discovery daemonset pods
	L2DiscoveryPods map[string]*v1core.Pod
	// Mapping between clusterwide interface index and Mac address
	ClusterMacs map[exports.IfClusterIndex]string
	// Mapping between clusterwide interface index and a simple integer (X) for readability in the graph
	ClusterIndexToInt map[exports.IfClusterIndex]int
	// Mapping between a cluster wide MAC address and a simple integer (X) for readability in the graph
	ClusterMacToInt map[string]int
	// Mapping between a Mac address and a cluster wide interface index
	ClusterIndexes map[string]exports.IfClusterIndex
	// indicates whether the L2discovery daemonset is created by the test suite (managed) or not
	L2DsMode L2DaemonsetMode
	// LANs identified in the graph
	LANs *[][]int
	// List of port receiving PTP frames (assuming valid GM signal received)
	PortsGettingPTP []*exports.PtpIf
	// interfaces to avoid when running the tests
	SkippedInterfaces []string
	// Indicates that the L2 configuration must be refreshed
	refresh bool
}

func (config L2DiscoveryConfig) String() string { //nolint:funlen
	var result strings.Builder

	result.WriteString("L2DiscoveryConfig:\n")
	result.WriteString(fmt.Sprintf("  MaxL2GraphSize: %d\n", config.MaxL2GraphSize))
	result.WriteString(fmt.Sprintf("  L2DsMode: %s\n", config.L2DsMode))
	result.WriteString(fmt.Sprintf("  refresh: %t\n", config.refresh))

	// PtpIfList
	result.WriteString("  PtpIfList: \n")
	for i, ptpIf := range config.PtpIfList {
		result.WriteString(fmt.Sprintf("    %d:\n%s\n", i, ptpIf.StringFull(ptpIfIndent)))
	}

	// PtpIfListUnfiltered
	result.WriteString("  PtpIfListUnfiltered: \n")
	for key, ptpIf := range config.PtpIfListUnfiltered {
		result.WriteString(fmt.Sprintf("    %s:\n%s\n", key, ptpIf.StringFull(ptpIfIndent)))
	}

	// L2DiscoveryPods
	result.WriteString("  L2DiscoveryPods: \n")
	for key, pod := range config.L2DiscoveryPods {
		result.WriteString(fmt.Sprintf("    [%s]: %s\n", key, pod.Name))
	}

	// ClusterMacs
	result.WriteString("  ClusterMacs:\n")
	for key, mac := range config.ClusterMacs {
		result.WriteString(fmt.Sprintf("    [%s]: %s\n", key, mac))
	}

	// ClusterIndexToInt
	result.WriteString("  ClusterIndexToInt:\n")
	for key, value := range config.ClusterIndexToInt {
		result.WriteString(fmt.Sprintf("    [%s]: %d\n", key, value))
	}

	// ClusterMacToInt
	result.WriteString("  ClusterMacToInt:\n")
	for key, value := range config.ClusterMacToInt {
		result.WriteString(fmt.Sprintf("    [%s]: %d\n", key, value))
	}

	// ClusterIndexes
	result.WriteString("  ClusterIndexes:\n")
	for key, value := range config.ClusterIndexes {
		result.WriteString(fmt.Sprintf("    [%s]: %s\n", key, value))
	}

	// LANs
	result.WriteString("  LANs: \n")
	if config.LANs != nil {
		for i, lan := range *config.LANs {
			result.WriteString(fmt.Sprintf("    [%d]: %v\n", i, lan))
		}
	} else {
		result.WriteString("nil\n")
	}

	// PortsGettingPTP
	result.WriteString("  PortsGettingPTP:\n")
	for i, ptpIf := range config.PortsGettingPTP {
		result.WriteString(fmt.Sprintf("    [%d]: %s\n", i, ptpIf))
	}

	// SkippedInterfaces
	result.WriteString("  SkippedInterfaces:\n")
	for i, iface := range config.SkippedInterfaces {
		result.WriteString(fmt.Sprintf("    [%d]: %s\n", i, iface))
	}
	return result.String()
}

var GlobalL2DiscoveryConfig L2DiscoveryConfig

func (config *L2DiscoveryConfig) GetPtpIfList() []*exports.PtpIf {
	return config.PtpIfList
}
func (config *L2DiscoveryConfig) GetPtpIfListUnfiltered() map[string]*exports.PtpIf {
	return config.PtpIfListUnfiltered
}
func (config *L2DiscoveryConfig) GetLANs() *[][]int {
	return config.LANs
}
func (config *L2DiscoveryConfig) GetPortsGettingPTP() []*exports.PtpIf {
	return config.PortsGettingPTP
}
func (config *L2DiscoveryConfig) SetL2Client(k8sClient kubernetes.Interface, restClient *rest.Config) {
	l2client.Set(k8sClient, restClient)
}

// Gets existing L2 configuration or creates a new one  (if refresh is set to true)
func (config *L2DiscoveryConfig) GetL2DiscoveryConfig(ptpInterfacesOnly, allIFs, useContainerCmds bool, l2DiscoveryImage string) (L2Info, error) {
	if GlobalL2DiscoveryConfig.refresh {
		err := GlobalL2DiscoveryConfig.DiscoverL2Connectivity(ptpInterfacesOnly, allIFs, useContainerCmds, l2DiscoveryImage)
		if err != nil {
			GlobalL2DiscoveryConfig.refresh = false
			return nil, fmt.Errorf("failed to discover L2 connectivity: %w", err)
		}
	}
	GlobalL2DiscoveryConfig.refresh = false
	return &GlobalL2DiscoveryConfig, nil
}

// Resets the L2 configuration
func (config *L2DiscoveryConfig) reset() {
	GlobalL2DiscoveryConfig.PtpIfList = []*exports.PtpIf{}
	GlobalL2DiscoveryConfig.L2DiscoveryPods = make(map[string]*v1core.Pod)
	GlobalL2DiscoveryConfig.ClusterMacs = make(map[exports.IfClusterIndex]string)
	GlobalL2DiscoveryConfig.ClusterIndexes = make(map[string]exports.IfClusterIndex)
	GlobalL2DiscoveryConfig.ClusterMacToInt = make(map[string]int)
	GlobalL2DiscoveryConfig.ClusterIndexToInt = make(map[exports.IfClusterIndex]int)
	GlobalL2DiscoveryConfig.ClusterIndexes = make(map[string]exports.IfClusterIndex)
	GlobalL2DiscoveryConfig.PtpIfListUnfiltered = make(map[string]*exports.PtpIf)
}

// Discovers the L2 connectivity using l2discovery daemonset
func (config *L2DiscoveryConfig) DiscoverL2Connectivity(ptpInterfacesOnly, allIFs, useContainerCmds bool, l2DiscoveryImage string) error {
	GlobalL2DiscoveryConfig.reset()
	GlobalL2DiscoveryConfig.InitSkippedInterfaces()
	// initializes clusterwide ptp interfaces
	var err error
	// Create L2 discovery daemonset
	config.L2DsMode = StringToL2Mode(os.Getenv("L2_DAEMONSET"))
	if config.L2DsMode == Managed {
		dummyMap := map[string]string{}
		env := []v1core.EnvVar{}
		if allIFs {
			env = append(env, v1core.EnvVar{Name: "L2DISCOVERY_ALLIFS", Value: "true"})
		}
		if useContainerCmds {
			env = append(env, v1core.EnvVar{Name: "L2DISCOVERY_USECONTAINERCMDS", Value: "true"})
		}
		_, err = daemonsets.CreateDaemonSet(L2DiscoveryDsName, L2DiscoveryNsName, L2DiscoveryContainerName, l2DiscoveryImage, dummyMap, env, timeoutDaemon,
			L2ContainerCPUReq, L2ContainerCPULim, L2ContainerMemReq, L2ContainerMemLim)
		if err != nil {
			return fmt.Errorf("error creating l2 discovery daemonset, err=%s", err)
		}
	}
	// Sleep a short time to allow discovery to happen (first report after 5s)
	time.Sleep(L2DiscoveryDuration)
	// Get the L2 topology pods
	err = GlobalL2DiscoveryConfig.getL2TopologyDiscoveryPods()
	if err != nil {
		return fmt.Errorf("could not get l2 discovery pods, err=%s", err)
	}
	err = config.getL2Disc(ptpInterfacesOnly)
	if err != nil {
		logrus.Errorf("error getting l2 discovery data, err=%s", err)
	}
	// Delete L2 discovery daemonset
	if config.L2DsMode == Managed {
		err = daemonsets.DeleteNamespaceIfPresent(L2DiscoveryNsName)
		if err != nil {
			logrus.Errorf("error deleting l2 discovery namespace, err=%s", err)
		}
	}
	// Create a graph from the discovered data
	err = config.createL2InternalGraph(ptpInterfacesOnly)
	if err != nil {
		return err
	}
	return nil
}

// Print database with all NICs
func (config *L2DiscoveryConfig) PrintAllNICs() {
	logrus.Info("List All NICS (node name: interface)")
	for index, aIf := range config.PtpIfList {
		logrus.Infof("%d %s", index, aIf)
	}

	for index, lan := range *config.LANs {
		aLog := fmt.Sprintf("LAN %d: ", index)
		for _, aIf := range lan {
			aLog += fmt.Sprintf("%s **** ", config.PtpIfList[aIf])
		}
		logrus.Debug(aLog)
	}
}

// Gets the latest topology reports from the l2discovery pods
func (config *L2DiscoveryConfig) getL2Disc(ptpInterfacesOnly bool) error {
	config.DiscoveryMap = make(map[string]map[string]map[string]*exports.Neighbors)
	index := 0
	keys := make([]string, 0, len(config.L2DiscoveryPods))

	for k := range config.L2DiscoveryPods {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		podLogs, _ := pods.GetLog(config.L2DiscoveryPods[k], config.L2DiscoveryPods[k].Spec.Containers[0].Name)
		indexReport := strings.LastIndex(podLogs, "JSON_REPORT")
		report := strings.Split(strings.Split(podLogs[indexReport:], `\n`)[0], "JSON_REPORT")[1]
		var discDataPerNode map[string]map[string]*exports.Neighbors
		if err := json.Unmarshal([]byte(report), &discDataPerNode); err != nil {
			return err
		}
		jsonData, err := json.MarshalIndent(discDataPerNode, "", "  ")
		if err != nil {
			return err
		}
		// print the formatted json data
		logrus.Tracef("key= %s discDataPerNode: %s", k, string(jsonData))
		if _, ok := config.DiscoveryMap[config.L2DiscoveryPods[k].Spec.NodeName]; !ok {
			config.DiscoveryMap[config.L2DiscoveryPods[k].Spec.NodeName] = make(map[string]map[string]*exports.Neighbors)
		}
		config.DiscoveryMap[config.L2DiscoveryPods[k].Spec.NodeName] = discDataPerNode

		config.createMaps(discDataPerNode, config.L2DiscoveryPods[k].Spec.NodeName, &index, ptpInterfacesOnly)
	}
	config.MaxL2GraphSize = index
	return nil
}

// Creates the Main topology graph
func (config *L2DiscoveryConfig) createL2InternalGraph(ptpInterfacesOnly bool) error {
	GlobalL2DiscoveryConfig.L2ConnectivityMap = graph.New(config.MaxL2GraphSize)
	keys := make([]string, 0, len(config.L2DiscoveryPods))

	for k := range config.L2DiscoveryPods {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		for iface, ifaceMap := range config.DiscoveryMap[config.L2DiscoveryPods[k].Spec.NodeName][ExperimentalEthertype] {
			for mac := range ifaceMap.Remote {
				if v, ok := config.ClusterIndexToInt[exports.IfClusterIndex{InterfaceName: iface, NodeName: config.L2DiscoveryPods[k].Spec.NodeName}]; ok {
					if w, ok := config.ClusterMacToInt[mac]; ok {
						if ptpInterfacesOnly &&
							(strings.Contains(config.PtpIfList[v].IfPci.Description, "Virtual") ||
								!config.PtpIfList[v].IfPTPCaps.HwRx ||
								!config.PtpIfList[v].IfPTPCaps.HwTx ||
								!config.PtpIfList[v].IfPTPCaps.HwRawClock ||
								!config.PtpIfList[w].IfPTPCaps.HwRx ||
								!config.PtpIfList[w].IfPTPCaps.HwTx ||
								!config.PtpIfList[w].IfPTPCaps.HwRawClock) {
							continue
						}
						config.L2ConnectivityMap.AddBoth(v, w)
					}
				}
			}
		}
	}
	// Init LANs
	out := graph.Components(config.L2ConnectivityMap)
	logrus.Infof("LANs connectivity map: %v", out)
	config.LANs = &out
	config.PrintAllNICs()

	logrus.Infof("NIC num: %d", config.MaxL2GraphSize)
	return nil
}

// Gets the grandmaster port by using L2 discovery data for ptp ethertype
func (config *L2DiscoveryConfig) getInterfacesReceivingPTP(ptpInterfacesOnly bool) {
	keys := make([]string, 0, len(config.L2DiscoveryPods))

	for k := range config.L2DiscoveryPods {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		for _, ifaceMap := range config.DiscoveryMap[config.L2DiscoveryPods[k].Spec.NodeName][PtpEthertype] {
			if len(ifaceMap.Remote) == 0 {
				continue
			}
			aPortGettingPTP := &exports.PtpIf{}
			aPortGettingPTP.Iface = ifaceMap.Local
			aPortGettingPTP.NodeName = config.L2DiscoveryPods[k].Spec.NodeName
			aPortGettingPTP.InterfaceName = aPortGettingPTP.Iface.IfName

			if ptpInterfacesOnly &&
				(strings.Contains(aPortGettingPTP.IfPci.Description, "Virtual") ||
					!aPortGettingPTP.IfPTPCaps.HwRx ||
					!aPortGettingPTP.IfPTPCaps.HwTx ||
					!aPortGettingPTP.IfPTPCaps.HwRawClock) {
				continue
			}
			config.PortsGettingPTP = append(config.PortsGettingPTP, aPortGettingPTP)
		}
	}
	logrus.Debugf("interfaces receiving PTP frames: %v", config.PortsGettingPTP)
}

// Creates Mapping tables between interfaces index, mac address, and graph integer indexes
func (config *L2DiscoveryConfig) createMaps(disc map[string]map[string]*exports.Neighbors, nodeName string, index *int, ptpInterfacesOnly bool) {
	config.updateMaps(disc, nodeName, index, ExperimentalEthertype, ptpInterfacesOnly)
	config.updateMaps(disc, nodeName, index, LocalInterfaces, ptpInterfacesOnly)
	config.getInterfacesReceivingPTP(ptpInterfacesOnly)
}

// retrieves interfaces to skip in the cluster
func (config *L2DiscoveryConfig) InitSkippedInterfaces() {
	ifs, isSet := os.LookupEnv("SKIP_INTERFACES")

	if isSet {
		tokens := strings.Split(ifs, ",")
		for _, token := range tokens {
			token = strings.TrimSpace(token)
			config.SkippedInterfaces = append(config.SkippedInterfaces, token)
		}
	}
	logrus.Infof("Will skip the following interfaces in every nodes: %v", config.SkippedInterfaces)
}

func (config *L2DiscoveryConfig) isSkipped(aIfToCheck string) bool {
	for _, ifName := range config.SkippedInterfaces {
		if aIfToCheck == ifName {
			return true
		}
	}
	return false
}

// updates Mapping tables between interfaces index, mac address, and graph integer indexes for a given ethertype
func (config *L2DiscoveryConfig) updateMaps(disc map[string]map[string]*exports.Neighbors, nodeName string, index *int, ethertype string, ptpInterfacesOnly bool) {
	// Sorting L2 discovery
	keys := make([]string, 0, len(disc[ethertype]))

	for k := range disc[ethertype] {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if _, ok := config.ClusterMacToInt[disc[ethertype][k].Local.IfMac.Data]; ok {
			continue
		}

		aInterface := exports.PtpIf{}
		aInterface.NodeName = nodeName
		aInterface.InterfaceName = disc[ethertype][k].Local.IfName
		aInterface.Iface = disc[ethertype][k].Local

		if ptpInterfacesOnly &&
			(strings.Contains(aInterface.IfPci.Description, "Virtual") ||
				!aInterface.IfPTPCaps.HwRx ||
				!aInterface.IfPTPCaps.HwTx ||
				!aInterface.IfPTPCaps.HwRawClock) {
			continue
		}
		config.PtpIfListUnfiltered[disc[ethertype][k].Local.IfMac.Data] = &aInterface

		if config.isSkipped(disc[ethertype][k].Local.IfName) {
			continue
		}

		// create maps
		config.ClusterMacToInt[disc[ethertype][k].Local.IfMac.Data] = *index
		config.ClusterIndexToInt[exports.IfClusterIndex{InterfaceName: disc[ethertype][k].Local.IfName, NodeName: nodeName}] = *index
		config.ClusterMacs[exports.IfClusterIndex{InterfaceName: disc[ethertype][k].Local.IfName, NodeName: nodeName}] = disc[ethertype][k].Local.IfMac.Data
		config.ClusterIndexes[disc[ethertype][k].Local.IfMac.Data] = exports.IfClusterIndex{InterfaceName: disc[ethertype][k].Local.IfName, NodeName: nodeName}

		config.PtpIfList = append(config.PtpIfList, &aInterface)
		(*index)++
	}
}

// Gets the list of l2discovery pods
func (config *L2DiscoveryConfig) getL2TopologyDiscoveryPods() error {
	aPodList, err := l2client.Client.K8sClient.CoreV1().Pods(L2DiscoveryNsName).List(context.Background(), metav1.ListOptions{LabelSelector: "name=l2discovery"})
	if err != nil {
		return fmt.Errorf("could not get list of linkloop pods, err=%s", err)
	}
	for index := range aPodList.Items {
		config.L2DiscoveryPods[aPodList.Items[index].Spec.NodeName] = &aPodList.Items[index]
	}
	return nil
}
