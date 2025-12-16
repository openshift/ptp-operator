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
	return mac.Data
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

type Neighbors struct {
	Local  Iface
	Remote map[string]bool
}

// Object representing a ptp interface within a cluster.
type PtpIf struct {
	// Index of the interface in the cluster (node/interface name)
	IfClusterIndex
	// Interface
	Iface
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
	result.WriteString(fmt.Sprintf(childIndentStr+"  IfSlaveType: %s", iface.IfSlaveType))

	return result.String()
}
