package exports

import (
	"fmt"
	"strings"
)

// L2Info interface for L2 discovery configuration
type L2Info interface {
	// list of cluster interfaces indexed with a simple integer (X) for readability in the graph
	GetPtpIfList() []*PtpIf
	// list of unfiltered cluster interfaces indexed with a simple integer (X) for readability in the graph
	GetPtpIfListUnfiltered() map[string]*PtpIf
	// LANs identified in the graph
	GetLANs() *[][]int
	// List of port receiving PTP frames (assuming valid GM signal received)
	GetPortsGettingPTP() []*PtpIf
}

// SolverConfig interface for graph solver configuration
type SolverConfig interface {
	// problem definition
	InitProblem(string, [][][]int, []int)
	// L2 configuration
	SetL2Config(L2Info)
	// Run solver on problem
	Run(string)
	// Prints all solutions
	PrintAllSolutions()
	// Prints first solution only
	PrintFirstSolution()
	// map storing solutions
	GetSolutions() map[string]*[][]int
}

type Mac struct {
	Data string
}

func (mac Mac) String() string {
	return strings.ToUpper(string([]byte(mac.Data)[0:2]) + ":" +
		string([]byte(mac.Data)[2:4]) + ":" +
		string([]byte(mac.Data)[4:6]) + ":" +
		string([]byte(mac.Data)[6:8]) + ":" +
		string([]byte(mac.Data)[8:10]) + ":" +
		string([]byte(mac.Data)[10:12]))
}

type PCIAddress struct {
	Device, Function, Description, Subsystem string
}

func (pci PCIAddress) String() string {
	return fmt.Sprintf("Device:%s Function:%s Description:%s Subsystem:%s", pci.Device, pci.Function, pci.Description, pci.Subsystem)
}

type PTPCaps struct {
	HwRx, HwTx, HwRawClock bool
}

func (caps PTPCaps) String() string {
	return fmt.Sprintf("HwRx:%t HwTx:%t HwRawClock:%t", caps.HwRx, caps.HwTx, caps.HwRawClock)
}

type Iface struct {
	IfName      string
	IfMac       Mac
	IfIndex     int
	IfPci       PCIAddress
	IfPTPCaps   PTPCaps
	IfUp        bool
	IfMaster    string
	IfSlaveType string
}

// PtpAnnounceData contains decoded fields from a PTP Announce message (IEEE 1588).
type PtpAnnounceData struct {
	DomainNumber            uint8  `json:"domainNumber"`
	GrandmasterPriority1    uint8  `json:"grandmasterPriority1"`
	ClockClass              uint8  `json:"clockClass"`
	ClockAccuracy           uint8  `json:"clockAccuracy"`
	OffsetScaledLogVariance uint16 `json:"offsetScaledLogVariance"`
	GrandmasterPriority2    uint8  `json:"grandmasterPriority2"`
	GrandmasterIdentity     string `json:"grandmasterIdentity"`
	StepsRemoved            uint16 `json:"stepsRemoved"`
	TimeSource              uint8  `json:"timeSource"`
}

func (a PtpAnnounceData) String() string {
	return fmt.Sprintf("Domain:%d ClockClass:%d Priority1:%d Priority2:%d ClockAccuracy:%d GMID:%s StepsRemoved:%d TimeSource:%d",
		a.DomainNumber, a.ClockClass, a.GrandmasterPriority1, a.GrandmasterPriority2,
		a.ClockAccuracy, a.GrandmasterIdentity, a.StepsRemoved, a.TimeSource)
}

type Neighbors struct {
	Local  Iface
	Remote map[string]bool
	// PTP Announce data received on this interface, keyed by grandmaster identity.
	// Multiple GMs may announce on the same LAN.
	PtpAnnounces map[string]*PtpAnnounceData `json:"ptpAnnounces,omitempty"`
}

// Object representing a ptp interface within a cluster.
type PtpIf struct {
	// Index of the interface in the cluster (node/interface name)
	IfClusterIndex
	// Interface
	Iface
	// PTP Announce data received on this interface, keyed by grandmaster identity.
	// nil if no announce received.
	Announces map[string]*PtpAnnounceData `json:"-"`
}

// Object used to index interfaces in a cluster
type IfClusterIndex struct {
	// interface name
	InterfaceName string
	// node name
	NodeName string
}

func (index IfClusterIndex) String() string {
	return fmt.Sprintf("%s_%s", index.NodeName, index.InterfaceName)
}

func (iface *PtpIf) String() string {
	return fmt.Sprintf("%s : %s", iface.NodeName, iface.IfName)
}

func (iface *PtpIf) String1() string {
	return fmt.Sprintf("index:%s mac:%s", iface.IfClusterIndex, iface.IfMac)
}

func (iface *PtpIf) StringFull(indent int) string {
	var result strings.Builder

	// Generate the indentation string
	const indentIncrement = 2
	indentStr := strings.Repeat(" ", indent)
	childIndentStr := strings.Repeat(" ", indent+indentIncrement)

	// IfClusterIndex fields
	result.WriteString(indentStr + "IfClusterIndex:\n")
	result.WriteString(fmt.Sprintf(childIndentStr+"  InterfaceName: %s\n", iface.InterfaceName))
	result.WriteString(fmt.Sprintf(childIndentStr+"  NodeName: %s\n", iface.NodeName))

	// Iface fields
	result.WriteString(indentStr + "Iface:\n")
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfName: %s\n", iface.IfName))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfMac: %s\n", iface.IfMac))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfIndex: %d\n", iface.IfIndex))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfPci: %s\n", iface.IfPci))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfPTPCaps: %s\n", iface.IfPTPCaps))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfUp: %t\n", iface.IfUp))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfMaster: %s\n", iface.IfMaster))
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfSlaveType: %s\n", iface.IfSlaveType))

	// Announce data
	if len(iface.Announces) > 0 {
		result.WriteString(indentStr + "Announces:\n")
		for gmID, announce := range iface.Announces {
			result.WriteString(fmt.Sprintf(childIndentStr+"  [%s]: %s\n", gmID, announce))
		}
	} else {
		result.WriteString(indentStr + "Announces: none")
	}

	return result.String()
}
