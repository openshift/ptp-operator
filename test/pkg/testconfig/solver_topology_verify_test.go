package testconfig

import (
	"testing"

	l2lib "github.com/redhat-cne/l2discovery-lib"
	"github.com/redhat-cne/l2discovery-lib/exports"
	solver "github.com/redhat-cne/l2discovery-lib/pkg/graphsolver"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// directConnL2Info implements l2lib.L2Info for the direct-connection CI
// topology created by scripts/configServers.sh: the 3 kind workers are
// connected directly to each other with point-to-point netdevsim links and
// there is no switch in between.
type directConnL2Info struct {
	ifList []*exports.PtpIf
	lans   [][]int
}

func (m *directConnL2Info) GetPtpIfList() []*exports.PtpIf                    { return m.ifList }
func (m *directConnL2Info) GetPtpIfListUnfiltered() map[string]*exports.PtpIf { return nil }
func (m *directConnL2Info) GetLANs() *[][]int                                 { return &m.lans }
func (m *directConnL2Info) GetPortsGettingPTP() []*exports.PtpIf              { return nil }
func (m *directConnL2Info) SetL2Client(kubernetes.Interface, *rest.Config)    {}

func (m *directConnL2Info) GetL2DiscoveryConfig(_, _, _ bool, _ string) (l2lib.L2Info, error) {
	return m, nil
}

// newDirectConnL2Info builds the L2 view of the CI cabling:
//
//	worker:   nic1 = ens1f0/ens1f1 (01), nic2 = ens2f0/ens2f1 (02)
//	worker2:  nic1 = ens3f0/ens3f1 (03), nic2 = ens4f0/ens4f1 (04)
//	worker3:  nic1 = ens5f0 (05), nic2 = ens6f0 (06), nic3 = ens7f0/ens7f1 (07)
//
//	links: worker.ens1f0<->worker2.ens3f0, worker.ens2f0<->worker2.ens3f1,
//	       worker.ens1f1<->worker3.ens5f0, worker.ens2f1<->worker3.ens6f0,
//	       worker2.ens4f0<->worker3.ens7f0, worker2.ens4f1<->worker3.ens7f1
func newDirectConnL2Info() *directConnL2Info {
	ifdefs := []struct{ node, ifName, pciDevice string }{
		{"worker", "ens1f0", "0000:1f:01"},  // 0
		{"worker", "ens1f1", "0000:1f:01"},  // 1
		{"worker", "ens2f0", "0000:1f:02"},  // 2
		{"worker", "ens2f1", "0000:1f:02"},  // 3
		{"worker2", "ens3f0", "0000:1f:03"}, // 4
		{"worker2", "ens3f1", "0000:1f:03"}, // 5
		{"worker2", "ens4f0", "0000:1f:04"}, // 6
		{"worker2", "ens4f1", "0000:1f:04"}, // 7
		{"worker3", "ens5f0", "0000:1f:05"}, // 8
		{"worker3", "ens6f0", "0000:1f:06"}, // 9
		{"worker3", "ens7f0", "0000:1f:07"}, // 10
		{"worker3", "ens7f1", "0000:1f:07"}, // 11
	}
	lans := [][]int{
		{0, 4},  // worker.ens1f0  <-> worker2.ens3f0 (GM <-> BC1 slave / OC)
		{2, 5},  // worker.ens2f0  <-> worker2.ens3f1 (GM <-> BC2 slave)
		{1, 8},  // worker.ens1f1  <-> worker3.ens5f0 (BC1 master <-> slave 1)
		{3, 9},  // worker.ens2f1  <-> worker3.ens6f0 (BC2 master <-> slave 2)
		{6, 10}, // worker2.ens4f0 <-> worker3.ens7f0 (GM <-> follower port 0)
		{7, 11}, // worker2.ens4f1 <-> worker3.ens7f1 (GM <-> follower port 1)
	}

	ifList := make([]*exports.PtpIf, 0, len(ifdefs))
	for _, d := range ifdefs {
		ifList = append(ifList, &exports.PtpIf{
			IfClusterIndex: exports.IfClusterIndex{InterfaceName: d.ifName, NodeName: d.node},
			Iface: exports.Iface{
				IfName:  d.ifName,
				IfIndex: len(ifList) + 1,
				IfPci:   exports.PCIAddress{Device: d.pciDevice, Function: ".0"},
				// PhcIndex < 0 keeps the WPC/GNSS (telco) problems unsolvable
				// on this topology, like on the real CI cluster.
				IfPTPCaps: exports.PTPCaps{PhcIndex: -1},
				IfUp:      true,
			},
		})
	}
	return &directConnL2Info{ifList: ifList, lans: lans}
}

// TestDirectConnectionTopologySolutions verifies that every CI test mode
// resolves on the direct-connection topology and that the expected role
// assignment is among the solutions.
func TestDirectConnectionTopologySolutions(t *testing.T) {
	mock := newDirectConnL2Info()
	solver.GlobalConfig.SetL2Config(mock)
	GlobalConfig.L2Config = mock

	initAndSolveProblems()

	// Every mode used by scripts/run-on-vm.sh must find at least one solution.
	for _, p := range []string{
		AlgoOCString,
		AlgoBCString,
		AlgoBCWithSlavesString,
		AlgoDualFollowerString,
		AlgoDualNicBCString,
		AlgoDualNicBCWithSlavesString,
	} {
		if sols := data.solutions[p]; sols == nil || len(*sols) == 0 {
			t.Errorf("problem %s: expected at least one solution on the CI topology, got none", p)
		} else {
			t.Logf("problem %s: %d solutions", p, len(*sols))
		}
	}

	// hasSolution reports whether a solution exists with the given
	// role -> node.interface assignment for all specified roles.
	hasSolution := func(problem string, want map[TestIfClockRoles]string) bool {
		mapping := *data.testClockRolesAlgoMapping[problem]
		for _, sol := range *data.solutions[problem] {
			match := true
			for role, wantIf := range want {
				idx := mapping[role]
				if idx >= len(sol) {
					match = false
					break
				}
				got := ifName(mock.ifList[sol[idx]])
				if got != wantIf {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
		return false
	}

	// Dual NIC BC with slaves: GM is a 2-port NIC on worker2 feeding both BC
	// slave ports, the BC is the dual NIC node, downstream slaves on worker3.
	dnbcWant := map[TestIfClockRoles]string{
		Grandmaster: "worker2.ens3f0", GrandmasterSibling: "worker2.ens3f1",
		BC1Slave: "worker.ens1f0", BC1Master: "worker.ens1f1",
		BC2Slave: "worker.ens2f0", BC2Master: "worker.ens2f1",
		Slave1: "worker3.ens5f0", Slave2: "worker3.ens6f0",
	}
	if !hasSolution(AlgoDualNicBCWithSlavesString, dnbcWant) {
		t.Errorf("expected dual NIC BC with slaves solution %v not found", dnbcWant)
	}

	// Dual follower: the 2-port GM on worker2 NIC2 feeds both ports of the
	// worker3 NIC3 dual follower clock.
	dfWant := map[TestIfClockRoles]string{
		Grandmaster: "worker2.ens4f0", GrandmasterSibling: "worker2.ens4f1",
		Slave1: "worker3.ens7f0", Slave2: "worker3.ens7f1",
	}
	if !hasSolution(AlgoDualFollowerString, dfWant) {
		t.Errorf("expected dual follower solution %v not found", dfWant)
	}

	// BC with slaves: GM on worker2, BC on worker dual port NIC1, slave on worker3.
	bcWant := map[TestIfClockRoles]string{
		Grandmaster: "worker2.ens3f0",
		BC1Slave:    "worker.ens1f0", BC1Master: "worker.ens1f1",
		Slave1: "worker3.ens5f0",
	}
	if !hasSolution(AlgoBCWithSlavesString, bcWant) {
		t.Errorf("expected BC with slaves solution %v not found", bcWant)
	}
}

func ifName(iface *exports.PtpIf) string {
	return iface.NodeName + "." + iface.IfName
}
