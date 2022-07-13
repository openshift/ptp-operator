package l2discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ptp-operator/test/utils"
	"github.com/openshift/ptp-operator/test/utils/client"
	"github.com/openshift/ptp-operator/test/utils/nodes"
	"github.com/openshift/ptp-operator/test/utils/pods"

	"github.com/openshift/ptp-operator/test/utils/daemonsets"
	l2 "github.com/test-network-function/l2discovery/export"

	"github.com/sirupsen/logrus"
	"github.com/yourbasic/graph"
	v1core "k8s.io/api/core/v1"
)

func init() {
	GlobalL2DiscoveryConfig.refresh = true
}

const (
	MaxL2GraphSize                 = 100
	experimentalEthertype          = "88b5"
	ptpEthertype                   = "88f7"
	L2DaemonsetManagedString       = "MANAGED"
	L2DaemonsetPreConfiguredString = "PRECONFIGURED"
	L2DiscoveryDsName              = "l2discovery"
	L2DiscoveryNsName              = "default"
	L2DiscoveryContainerName       = "l2discovery"
	timeoutDaemon                  = time.Second * 60
	L2DiscoveryDuration            = time.Second * 15
	L2DiscoveryDurationSNO         = time.Second * 15
	l2DiscoveryImage               = "quay.io/testnetworkfunction/l2discovery:v2"
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

var GlobalL2DiscoveryConfig L2DiscoveryConfig

// Object used to index interfaces in a cluster
type IfClusterIndex struct {
	// interface name
	IfName string
	// node name
	NodeName string
}

// Object representing a ptp interface within a cluster.
type PtpIf struct {
	// Mac address of the Ethernet interface
	MacAddress string
	// Index of the interface in the cluster (node/interface name)
	IfClusterIndex
	// PCI address
	IfPci l2.PCIAddress
}

// Object representing the calculated clock configuration
type ClockConfig struct {
	// Grandmaster selected cluster interface
	Grandmaster *PtpIf
	// Slave selected cluster interface (OC)
	Slave *PtpIf
	// BC Master selected interface (BC)
	BcMaster []*PtpIf
	// BC slave selected interface (BC)
	BcSlave []*PtpIf
}

type L2DiscoveryConfig struct {
	// Map of L2 topology as discovered by L2 discovery mechanism
	DiscoveryMap map[string]map[string]map[string]*l2.Neighbors
	// L2 topology graph created from discovery map. This is the main internal graph
	L2ConnectivityMap *graph.Mutable
	// list of cluster interfaces indexed with a simple integer (X) for readability in the graph
	PtpIfList []*PtpIf
	// list of L2discovery daemonset pods
	L2DiscoveryPods map[string]*v1core.Pod
	// Mapping between clusterwide interface index and Mac address
	ClusterMacs map[IfClusterIndex]string
	// Mapping between clusterwide interface index and a simple integer (X) for readability in the graph
	ClusterIndexToInt map[IfClusterIndex]int
	// Mapping between a cluster wide MAC address and a simple integer (X) for readability in the graph
	ClusterMacToInt map[string]int
	// Mapping between a Mac address and a cluster wide interface index
	ClusterIndexes map[string]IfClusterIndex
	// Object holding the resolved clock configuration
	BestClockConfig ClockConfig
	// 2D Map holding the valid ptp interfaces as reported by the ptp-operator api. map[ <node name>]map[<interface name>]
	ptpInterfaces map[string]map[string]bool
	// indicates whether the L2discovery daemonset is created by the test suite (managed) or not
	L2DsMode L2DaemonsetMode
	// Indicates that the L2 configuration must be refreshed
	refresh bool
}

func (index IfClusterIndex) String() string {
	return fmt.Sprintf("%s_%s", index.NodeName, index.IfName)
}
func (iface PtpIf) String() string {
	return fmt.Sprintf("index:%s mac:%s", iface.IfClusterIndex, iface.MacAddress)
}

// Gets existing L2 configuration or creates a new one  (if refresh is set to true)
func GetL2DiscoveryConfig() (config *L2DiscoveryConfig, err error) {
	if GlobalL2DiscoveryConfig.refresh {
		err := GlobalL2DiscoveryConfig.DiscoverL2Connectivity(client.Client)
		if err != nil {
			GlobalL2DiscoveryConfig.refresh = false
			return config, fmt.Errorf("could not get L2 config")
		}
	}

	GlobalL2DiscoveryConfig.refresh = false
	return &GlobalL2DiscoveryConfig, nil
}

// Resets the L2 configuration
func (config *L2DiscoveryConfig) reset() {
	GlobalL2DiscoveryConfig.L2ConnectivityMap = graph.New(MaxL2GraphSize)
	GlobalL2DiscoveryConfig.PtpIfList = []*PtpIf{}
	GlobalL2DiscoveryConfig.L2DiscoveryPods = make(map[string]*v1core.Pod)
	GlobalL2DiscoveryConfig.ClusterMacs = make(map[IfClusterIndex]string)
	GlobalL2DiscoveryConfig.ClusterIndexes = make(map[string]IfClusterIndex)
	GlobalL2DiscoveryConfig.ClusterMacToInt = make(map[string]int)
	GlobalL2DiscoveryConfig.ClusterIndexToInt = make(map[IfClusterIndex]int)
	GlobalL2DiscoveryConfig.ClusterIndexes = make(map[string]IfClusterIndex)
}

// Discovers the L2 connectivity using l2discovery daemonset
func (config *L2DiscoveryConfig) DiscoverL2Connectivity(client *client.ClientSet) error {
	GlobalL2DiscoveryConfig.reset()

	nodeDevicesList, err := client.NodePtpDevices(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(nodeDevicesList.Items) == 0 {
		return fmt.Errorf("Zero nodes found")
	}

	// initializes clusterwide ptp interfaces
	config.ptpInterfaces, err = buildPtpIfaces(client)
	if err != nil {
		logrus.Errorf("could not retrieve ptp interface list")
	}

	// Create L2 discovery daemonset
	config.L2DsMode = StringToL2Mode(os.Getenv("L2_DAEMONSET"))
	if config.L2DsMode == Managed {
		_, err = daemonsets.CreateDaemonSet(L2DiscoveryDsName, L2DiscoveryNsName, L2DiscoveryContainerName, l2DiscoveryImage, timeoutDaemon)
		if err != nil {
			logrus.Errorf("error creating l2 discovery daemonset, err=%s", err)
		}
	}

	// Get the L2 topology pods
	err = GlobalL2DiscoveryConfig.getL2TopologyDiscoveryPods()
	if err != nil {
		return fmt.Errorf("could not get linkloop pods, err=%s", err)
	}

	isSingleNode, err := nodes.IsSingleNodeCluster()
	if err != nil {
		return err
	}

	if isSingleNode {
		// Sleep a short time to allow discovery to happen (first report after 5s)
		time.Sleep(L2DiscoveryDurationSNO)
	} else {
		// Sleep a short time to allow discovery to happen (first report after 5s)
		time.Sleep(L2DiscoveryDuration)
	}

	err = config.getL2Disc()
	if err != nil {
		logrus.Errorf("error getting l2 discovery data, err=%s", err)
	}

	// Delete L2 discovery daemonset
	if config.L2DsMode == Managed {
		err = daemonsets.DeleteDaemonSet(L2DiscoveryDsName, L2DiscoveryNsName)
		if err != nil {
			logrus.Errorf("error deleting l2 discovery daemonset, err=%s", err)
		}
	}

	if !isSingleNode {
		// Create a graph from the discovered data
		err = config.createL2InternalGraph()
		if err != nil {
			return err
		}
		// resolve the graph and find a valid clock configuration
		config.createConfigurations()
	} else {
		config.createSNOConfiguration()
	}

	return nil
}

func (config *L2DiscoveryConfig) createConfigurations() {

	out := graph.Components(config.L2ConnectivityMap)
	logrus.Infof("%v", out)

	// BC and Dual Nic BC capable
	for index1, island1 := range out {
		// Find an island with more than 1 member
		if len(island1) > 1 {
			// For each of these, look for another island with more than 1 member
			for _, ifIndex1 := range island1 {
				for index2, island2 := range out {
					// for each other island that has len > 1
					if len(island2) > 1 && index1 != index2 {
						// Look through the second island members to find a boundary clock
						for _, ifIndex2 := range island2 {
							// Both ports in the boundary clock belong to the same NIC but belong to 2 different islands
							if !SameNic(config.PtpIfList[ifIndex1], config.PtpIfList[ifIndex2]) {
								continue
							}
							config.BestClockConfig.Slave = config.PtpIfList[ifIndex1]
							config.BestClockConfig.BcMaster = append(config.BestClockConfig.BcMaster, config.PtpIfList[ifIndex2])
							// pick a grandmaster
							for _, indexExtra1 := range island1 {
								if indexExtra1 != ifIndex1 &&
									config.PtpIfList[ifIndex1].NodeName != config.PtpIfList[indexExtra1].NodeName {
									config.BestClockConfig.Grandmaster = config.PtpIfList[indexExtra1]
									break
								}
							}
							// pick a BC slave
							for _, indexExtra2 := range island2 {
								if indexExtra2 != ifIndex2 &&
									config.PtpIfList[ifIndex2].NodeName != config.PtpIfList[indexExtra2].NodeName &&
									config.BestClockConfig.Grandmaster.NodeName != config.PtpIfList[indexExtra2].NodeName {
									config.BestClockConfig.BcSlave = append(config.BestClockConfig.BcSlave, config.PtpIfList[indexExtra2])
								}
							}
							return
						}
					}
				}
			}
		}
	}
	// OC capable
	for _, island1 := range out {
		// Pick a valid OC config
		if len(island1) > 1 {
			config.BestClockConfig.Grandmaster = config.PtpIfList[island1[0]]
			config.BestClockConfig.Slave = config.PtpIfList[island1[1]]
			break
		}
	}
}

// Determines if 2 interfaces (ports) belong to the same NIC
func SameNic(ifaceName1, ifaceName2 *PtpIf) bool {
	if ifaceName1.IfClusterIndex.NodeName != ifaceName2.IfClusterIndex.NodeName {
		return false
	}
	return ifaceName1.IfPci.Device != "" && ifaceName1.IfPci.Device == ifaceName2.IfPci.Device
}

// Gets the latest topology reports from the l2discovery pods
func (config *L2DiscoveryConfig) getL2Disc() error {
	config.DiscoveryMap = make(map[string]map[string]map[string]*l2.Neighbors)
	index := 0
	for _, aPod := range config.L2DiscoveryPods {
		podLogs, _ := pods.GetLog(aPod, aPod.Spec.Containers[0].Name)
		indexReport := strings.LastIndex(podLogs, "JSON_REPORT")
		report := strings.Split(strings.Split(podLogs[indexReport:], `\n`)[0], "JSON_REPORT")[1]
		var discDataPerNode map[string]map[string]*l2.Neighbors
		if err := json.Unmarshal([]byte(report), &discDataPerNode); err != nil {
			return err
		}

		if _, ok := config.DiscoveryMap[aPod.Spec.NodeName]; !ok {
			config.DiscoveryMap[aPod.Spec.NodeName] = make(map[string]map[string]*l2.Neighbors)
		}
		config.DiscoveryMap[aPod.Spec.NodeName] = discDataPerNode

		config.createMaps(discDataPerNode, aPod.Spec.NodeName, &index)
	}
	return nil
}

// Creates the Main topology graph
func (config *L2DiscoveryConfig) createL2InternalGraph() error {
	for _, aPod := range config.L2DiscoveryPods {
		for iface, ifaceMap := range config.DiscoveryMap[aPod.Spec.NodeName][experimentalEthertype] {
			for mac := range ifaceMap.Remote {
				v := config.ClusterIndexToInt[IfClusterIndex{IfName: iface, NodeName: aPod.Spec.NodeName}]
				w := config.ClusterMacToInt[mac]

				if _, ok := config.ptpInterfaces[config.PtpIfList[v].NodeName][config.PtpIfList[v].IfName]; ok {
					if _, ok := config.ptpInterfaces[config.PtpIfList[w].NodeName][config.PtpIfList[w].IfName]; ok {
						// only add ptp capable interfaces
						config.L2ConnectivityMap.AddBoth(v, w)
					}
				}
			}
		}
	}
	return nil
}

// Gets the grandmaster port by using L2 discovery data for ptp ethertype
func (config *L2DiscoveryConfig) createSNOConfiguration() {
	for _, aPod := range config.L2DiscoveryPods {
		for _, ifaceMap := range config.DiscoveryMap[aPod.Spec.NodeName][ptpEthertype] {
			if len(ifaceMap.Remote) == 0 {
				continue
			}
			var aPortGettingPTP PtpIf
			aPortGettingPTP.IfName = ifaceMap.Local.IfName
			aPortGettingPTP.NodeName = aPod.Spec.NodeName
			config.BestClockConfig.Slave = &aPortGettingPTP

			anotherPortGettingPTP := config.findAnotherPortInSamePTPNic(&aPortGettingPTP)
			config.BestClockConfig.BcMaster = []*PtpIf{anotherPortGettingPTP}
			return
		}
	}
}

// Looks for a second port in a NIC
func (config *L2DiscoveryConfig) findAnotherPortInSamePTPNic(firstIf *PtpIf) (secondIf *PtpIf) {
	for secondIfName := range config.ptpInterfaces[firstIf.NodeName] {
		secondIf = &PtpIf{IfClusterIndex: IfClusterIndex{IfName: secondIfName, NodeName: firstIf.NodeName},
			MacAddress: ""}
		if firstIf.IfName != secondIfName &&
			SameNic(firstIf, secondIf) {
			return secondIf
		}
	}
	logrus.Warnf("Could not find a port belonging to the same nic as port %s: ", secondIf)
	return secondIf
}

// Creates Mapping tables between interfaces index, mac address, and graph integer indexes
func (config *L2DiscoveryConfig) createMaps(disc map[string]map[string]*l2.Neighbors, nodeName string, index *int) {
	for _, ifaceData := range disc[experimentalEthertype] {
		config.ClusterMacToInt[ifaceData.Local.IfMac.Data] = *index
		config.ClusterIndexToInt[IfClusterIndex{IfName: ifaceData.Local.IfName, NodeName: nodeName}] = *index
		config.ClusterMacs[IfClusterIndex{IfName: ifaceData.Local.IfName, NodeName: nodeName}] = ifaceData.Local.IfMac.Data
		config.ClusterIndexes[ifaceData.Local.IfMac.Data] = IfClusterIndex{IfName: ifaceData.Local.IfName, NodeName: nodeName}
		aInterface := PtpIf{}
		aInterface.NodeName = nodeName
		aInterface.IfName = ifaceData.Local.IfName
		aInterface.MacAddress = ifaceData.Local.IfMac.Data
		aInterface.IfPci = ifaceData.Local.IfPci
		config.PtpIfList = append(config.PtpIfList, &aInterface)
		(*index)++
	}
}

// Gets the list of l2discovery pods
func (config *L2DiscoveryConfig) getL2TopologyDiscoveryPods() error {
	aPodList, err := client.Client.CoreV1().Pods(L2DiscoveryNsName).List(context.Background(), metav1.ListOptions{LabelSelector: "name=l2discovery"})
	if err != nil {
		return fmt.Errorf("could not get list of linkloop pods, err=%s", err)
	}
	for index := range aPodList.Items {
		config.L2DiscoveryPods[aPodList.Items[index].Spec.NodeName] = &aPodList.Items[index]
	}
	return nil
}

// Create a list of valid PTP interfaces as reported by the ptp-operator API
func buildPtpIfaces(aClient *client.ClientSet) (ptpIfaces map[string]map[string]bool, err error) {
	nodeDevicesList, err := aClient.NodePtpDevices(utils.PtpLinuxDaemonNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	if len(nodeDevicesList.Items) == 0 {
		return nil, fmt.Errorf("Zero nodes found")
	}

	ptpIfaces = make(map[string]map[string]bool)

	nodesList, err := nodes.MatchingOptionalSelectorPTP(nodeDevicesList.Items)
	if err != nil {
		logrus.Errorf("error matching optional selectors, err=%s", err)
	}
	for index := range nodesList {
		if _, ok := ptpIfaces[nodesList[index].Name]; !ok {
			ptpIfaces[nodesList[index].Name] = make(map[string]bool)
		}
		for _, iface := range nodesList[index].Status.Devices {
			ptpIfaces[nodesList[index].Name][iface.Name] = true
		}
	}
	logrus.Infof("%v", ptpIfaces)
	return ptpIfaces, nil
}
