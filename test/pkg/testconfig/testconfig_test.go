package testconfig

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	testclient "github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	l2lib "github.com/redhat-cne/l2discovery-lib"
	"github.com/redhat-cne/l2discovery-lib/exports"
	solver "github.com/redhat-cne/l2discovery-lib/pkg/graphsolver"
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// mockL2Info implements l2lib.L2Info for unit testing preferDiffNodeSolution.
type mockL2Info struct {
	ifList []*exports.PtpIf
}

func (m *mockL2Info) GetPtpIfList() []*exports.PtpIf                    { return m.ifList }
func (m *mockL2Info) GetPtpIfListUnfiltered() map[string]*exports.PtpIf { return nil }
func (m *mockL2Info) GetLANs() *[][]int                                 { return nil }
func (m *mockL2Info) GetPortsGettingPTP() []*exports.PtpIf              { return nil }
func (m *mockL2Info) SetL2Client(kubernetes.Interface, *rest.Config)    {}

func (m *mockL2Info) GetL2DiscoveryConfig(_, _, _ bool, _ string) (l2lib.L2Info, error) {
	return m, nil
}

func TestClockNodeInfo(t *testing.T) {
	config := func(name, node string) *ptpDiscoveryRes {
		return (*ptpDiscoveryRes)(&ptpv1.PtpConfig{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: ptpv1.PtpConfigSpec{Recommend: []ptpv1.PtpRecommend{{
				Match: []ptpv1.MatchRule{{NodeName: &node}},
			}}},
		})
	}
	pod := func(name, node string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: pkg.PtpLinuxDaemonNamespace, Labels: map[string]string{"app": "linuxptp-daemon"}},
			Spec:       corev1.PodSpec{NodeName: node},
		}
	}

	originalClient := testclient.Client.Interface
	t.Cleanup(func() { testclient.Client.Interface = originalClient })
	testclient.Client.Interface = k8sfake.NewSimpleClientset(pod("ptp-node-a", "node-a"), pod("ptp-node-b", "node-b"))
	info, err := (TestConfig{
		DiscoveredGrandMasterPtpConfig:    config("grandmaster", "node-a"),
		DiscoveredClockUnderTestPtpConfig: config("clock-under-test", "node-b"),
	}).ClockNodeInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info["Grandmaster Clock"] != "node-a" || info["Clock Under Test"] != "node-b" {
		t.Fatalf("unexpected clock node info: %v", info)
	}
}

func makePtpIf(node, iface string) *exports.PtpIf {
	return &exports.PtpIf{
		IfClusterIndex: exports.IfClusterIndex{NodeName: node, InterfaceName: iface},
	}
}

func TestHasGNSSDeviceWrapperFindsSibling(t *testing.T) {
	config := &mockL2Info{ifList: []*exports.PtpIf{
		{IfClusterIndex: exports.IfClusterIndex{NodeName: "node1", InterfaceName: "ens1f0"}, Iface: exports.Iface{IfPci: exports.PCIAddress{Device: "0000:01:00"}, IfPTPCaps: exports.PTPCaps{PhcIndex: 0}}},
		{IfClusterIndex: exports.IfClusterIndex{NodeName: "node1", InterfaceName: "ens1f1"}, Iface: exports.Iface{IfPci: exports.PCIAddress{Device: "0000:01:00"}, IfPTPCaps: exports.PTPCaps{PhcIndex: 0, GnssDevice: "gnss0"}}},
		{IfClusterIndex: exports.IfClusterIndex{NodeName: "node2", InterfaceName: "ens1f0"}, Iface: exports.Iface{IfPci: exports.PCIAddress{Device: "0000:01:00"}, IfPTPCaps: exports.PTPCaps{PhcIndex: 0}}},
	}}

	if !solver.HasGNSSDeviceWrapper(config, 0) {
		t.Fatal("expected GNSS device on sibling interface to satisfy selected interface")
	}
	if solver.HasGNSSDeviceWrapper(config, 2) {
		t.Fatal("GNSS device from another node must not satisfy selected interface")
	}
}

// setupPreferDiffNode wires up the package-level data and GlobalConfig so
// preferDiffNodeSolution can run without the full solver/discovery stack.
func setupPreferDiffNode(ifList []*exports.PtpIf, roleMap []int, solutions [][]int, problem string) {
	data.solutions = map[string]*[][]int{problem: &solutions}
	data.testClockRolesAlgoMapping = map[string]*[]int{problem: &roleMap}
	GlobalConfig.L2Config = &mockL2Info{ifList: ifList}
}

// ocRoleMap returns the real OC role mapping: Slave1=0, Grandmaster=1
func ocRoleMap() []int {
	m := make([]int, NumTestClockRoles)
	m[int(Slave1)] = 0
	m[int(Grandmaster)] = 1
	return m
}

// bcWithSlavesRoleMap returns the real BC-with-slaves mapping:
// Slave1=0, BC1Master=1, BC1Slave=2, Grandmaster=3
func bcWithSlavesRoleMap() []int {
	m := make([]int, NumTestClockRoles)
	m[int(Slave1)] = 0
	m[int(BC1Master)] = 1
	m[int(BC1Slave)] = 2
	m[int(Grandmaster)] = 3
	return m
}

func TestPreferDiffNodeSolution(t *testing.T) {
	// Two-node lab topology modeled after real PTP CI clusters.
	// Each node has an E810 NIC with two ports on the same PCI device.
	twoNodeIfList := []*exports.PtpIf{
		makePtpIf("cnfdg3.ptp.eng.rdu2.dc.redhat.com", "ens5f0"),  // 0
		makePtpIf("cnfdg3.ptp.eng.rdu2.dc.redhat.com", "ens5f1"),  // 1
		makePtpIf("cnfdf32.ptp.eng.rdu2.dc.redhat.com", "ens5f0"), // 2
		makePtpIf("cnfdf32.ptp.eng.rdu2.dc.redhat.com", "ens5f1"), // 3
	}

	tests := []struct {
		name      string
		problem   string
		ifList    []*exports.PtpIf
		roleMap   []int
		solutions [][]int
		roles     []TestIfClockRoles
		wantIdx   int
	}{
		{
			// OC: GM on one node, Slave on another — first solution already good
			name:    "OC with GM and Slave on different nodes picks first solution",
			problem: AlgoOCString,
			ifList:  twoNodeIfList,
			roleMap: ocRoleMap(),
			solutions: [][]int{
				{2, 0}, // sol 0: Slave1->cnfdf32/ens5f0, GM->cnfdg3/ens5f0 — different nodes
				{3, 1}, // sol 1: Slave1->cnfdf32/ens5f1, GM->cnfdg3/ens5f1 — different nodes
			},
			roles:   []TestIfClockRoles{Grandmaster, Slave1},
			wantIdx: 0,
		},
		{
			// OC: first solution has GM and Slave co-located, second splits them
			name:    "OC skips same-node solution for GM and Slave",
			problem: AlgoOCString,
			ifList:  twoNodeIfList,
			roleMap: ocRoleMap(),
			solutions: [][]int{
				{0, 1}, // sol 0: Slave1->cnfdg3/ens5f0, GM->cnfdg3/ens5f1 — same node
				{0, 2}, // sol 1: Slave1->cnfdg3/ens5f0, GM->cnfdf32/ens5f0 — different nodes
			},
			roles:   []TestIfClockRoles{Grandmaster, Slave1},
			wantIdx: 1,
		},
		{
			// BC (without slaves): GM and BC1Slave co-located in first solution,
			// preferDiffNodeSolution skips to the second.
			name:    "BC skips same-node solution for GM and BC1Slave",
			problem: AlgoBCString,
			ifList:  twoNodeIfList,
			roleMap: func() []int {
				m := make([]int, NumTestClockRoles)
				m[int(BC1Slave)] = 0
				m[int(BC1Master)] = 1
				m[int(Grandmaster)] = 2
				return m
			}(),
			solutions: [][]int{
				// sol 0: BC1Slave=cnfdg3, BC1Master=cnfdg3, GM=cnfdg3 — all same node
				{0, 1, 1},
				// sol 1: BC1Slave=cnfdg3, BC1Master=cnfdg3, GM=cnfdf32 — GM on different node
				{0, 1, 2},
			},
			roles:   []TestIfClockRoles{Grandmaster, BC1Slave},
			wantIdx: 1,
		},
		{
			// OC single-node lab: only one node available. OC has no hard same-node
			// constraint, so the solver may produce same-node solutions.
			// preferDiffNodeSolution falls back to FirstSolution.
			name:    "OC single-node lab falls back to FirstSolution",
			problem: AlgoOCString,
			ifList: []*exports.PtpIf{
				makePtpIf("cnfdf32.ptp.eng.rdu2.dc.redhat.com", "ens5f0"), // 0
				makePtpIf("cnfdf32.ptp.eng.rdu2.dc.redhat.com", "ens5f1"), // 1
			},
			roleMap: ocRoleMap(),
			solutions: [][]int{
				{0, 1}, // sol 0: Slave1=cnfdf32, GM=cnfdf32 — same node
				{1, 0}, // sol 1: Slave1=cnfdf32, GM=cnfdf32 — same node
			},
			roles:   []TestIfClockRoles{Grandmaster, Slave1},
			wantIdx: FirstSolution,
		},
		{
			// BC with slaves: 3 roles across a two-node cluster.
			// With only 2 nodes and 3 checked roles, no solution can split all three,
			// so it falls back to FirstSolution.
			name:    "BC with slaves falls back when 3 roles cannot span 2 nodes",
			problem: AlgoBCWithSlavesString,
			ifList:  twoNodeIfList,
			roleMap: bcWithSlavesRoleMap(),
			solutions: [][]int{
				// sol 0: Slave1=cnfdg3, BC1Master=cnfdg3, BC1Slave=cnfdg3, GM=cnfdf32
				//   GM differs from BC1Slave, but Slave1 == BC1Slave node
				{0, 1, 0, 2},
				// sol 1: Slave1=cnfdf32, BC1Master=cnfdf32, BC1Slave=cnfdf32, GM=cnfdg3
				{2, 3, 2, 0},
			},
			roles:   []TestIfClockRoles{Grandmaster, BC1Slave, Slave1},
			wantIdx: FirstSolution,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupPreferDiffNode(tt.ifList, tt.roleMap, tt.solutions, tt.problem)
			got := preferDiffNodeSolution(tt.problem, tt.roles...)
			if got != tt.wantIdx {
				t.Errorf("preferDiffNodeSolution() = %d, want %d", got, tt.wantIdx)
			}
		})
	}
}

// TestPreferDiffNodeSolution_ThreeNodeCluster models a 3-node lab where all
// roles can land on distinct nodes. This is the ideal multi-node scenario for
// BC-with-slaves (Grandmaster, BC1Slave, Slave1 each on a separate node).
func TestPreferDiffNodeSolution_ThreeNodeCluster(t *testing.T) {
	ifList := []*exports.PtpIf{
		makePtpIf("cnfdg3.ptp.eng.rdu2.dc.redhat.com", "ens5f0"),  // 0
		makePtpIf("cnfdg3.ptp.eng.rdu2.dc.redhat.com", "ens5f1"),  // 1
		makePtpIf("cnfdf32.ptp.eng.rdu2.dc.redhat.com", "ens5f0"), // 2
		makePtpIf("cnfdf32.ptp.eng.rdu2.dc.redhat.com", "ens5f1"), // 3
		makePtpIf("cnfdf33.ptp.eng.rdu2.dc.redhat.com", "ens5f0"), // 4
		makePtpIf("cnfdf33.ptp.eng.rdu2.dc.redhat.com", "ens5f1"), // 5
	}

	roleMap := bcWithSlavesRoleMap()

	solutions := [][]int{
		// sol 0: Slave1=cnfdg3, BC1Master=cnfdg3, BC1Slave=cnfdg3, GM=cnfdf32
		//   Slave1 and BC1Slave on same node (cnfdg3)
		{0, 1, 0, 2},
		// sol 1: Slave1=cnfdf33, BC1Master=cnfdf32, BC1Slave=cnfdf32, GM=cnfdg3
		//   BC1Slave and Slave1 on different nodes, but BC1Master and BC1Slave share cnfdf32
		//   (BC1Master is not in the checked roles, so this is fine)
		//   GM=cnfdg3, BC1Slave=cnfdf32, Slave1=cnfdf33 — all checked roles on different nodes
		{4, 3, 2, 0},
	}

	setupPreferDiffNode(ifList, roleMap, solutions, AlgoBCWithSlavesString)
	got := preferDiffNodeSolution(AlgoBCWithSlavesString, Grandmaster, BC1Slave, Slave1)
	if got != 1 {
		t.Errorf("preferDiffNodeSolution() = %d, want 1", got)
	}
}

const (
	config1    = "config1"
	config2    = "config2"
	config3    = "config3"
	namespace1 = "namespace1"
)

func TestGetDesiredConfig(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		legacyMode  string
		forceUpdate bool
		want        TestConfig
	}{
		// TODO: Add test cases.
		{
			name:        "Discovery",
			forceUpdate: false,
			mode:        DiscoveryString,

			want: TestConfig{
				Discovery,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
		{
			name:        "OC",
			forceUpdate: true,
			mode:        OrdinaryClockString,
			want: TestConfig{
				OrdinaryClock,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
		{
			name:        "BC",
			forceUpdate: true,
			mode:        BoundaryClockString,
			want: TestConfig{
				BoundaryClock,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
		{
			name:        "DualNICBC",
			forceUpdate: true,
			mode:        DualNICBoundaryClockString,
			want: TestConfig{
				DualNICBoundaryClock,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
		{
			name:        "test update",
			forceUpdate: false,
			mode:        OrdinaryClockString,
			want: TestConfig{
				DualNICBoundaryClock,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
		{
			name:        "legacy discovery",
			forceUpdate: true,
			mode:        NoneString,
			legacyMode:  legacyDiscoveryString,
			want: TestConfig{
				Discovery,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
		{
			name:        "no config",
			forceUpdate: true,
			mode:        NoneString,
			legacyMode:  NoneString,
			want: TestConfig{
				OrdinaryClock,
				None,
				InitStatus,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				false,
			},
		},
	}
	Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("DISCOVERY_MODE", tt.legacyMode)
			os.Setenv("PTP_TEST_MODE", tt.mode)
			if got := GetDesiredConfig(tt.forceUpdate); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetDesiredConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetFullDiscoveredConfig(t *testing.T) {
	type args struct {
		namespace string
		mode      PTPMode
	}
	tests := []struct {
		name string
		args args
		want TestConfig
	}{
		// TODO: Add test cases.
		{
			name: "Ordinary clock",
			args: args{namespace: namespace1,
				mode: OrdinaryClock},
			want: TestConfig{
				PtpModeDesired:                    None,
				PtpModeDiscovered:                 OrdinaryClock,
				Status:                            DiscoverySuccessStatus,
				DiscoveredClockUnderTestPtpConfig: (*ptpDiscoveryRes)(mockPtpConfig(config1, namespace1, ptpv1.Slave, OrdinaryClock)),
				DiscoveredClockUnderTestSecondaryPtpConfig: nil,
			},
		},
		{
			name: "Boundary clock",
			args: args{namespace: namespace1,
				mode: BoundaryClock},
			want: TestConfig{
				PtpModeDesired:                    None,
				PtpModeDiscovered:                 BoundaryClock,
				Status:                            DiscoverySuccessStatus,
				DiscoveredClockUnderTestPtpConfig: (*ptpDiscoveryRes)(mockPtpConfig(config2, namespace1, ptpv1.Slave, BoundaryClock)),
				DiscoveredClockUnderTestSecondaryPtpConfig: nil,
			},
		},
		{
			name: "Dual NIC Boundary clock",
			args: args{namespace: namespace1,
				mode: DualNICBoundaryClock},
			want: TestConfig{
				PtpModeDesired:                    None,
				PtpModeDiscovered:                 DualNICBoundaryClock,
				Status:                            DiscoverySuccessStatus,
				DiscoveredClockUnderTestPtpConfig: (*ptpDiscoveryRes)(mockPtpConfig(config2, namespace1, ptpv1.Slave, BoundaryClock)),
				DiscoveredClockUnderTestSecondaryPtpConfig: (*ptpDiscoveryRes)(mockPtpConfig(config3, namespace1, ptpv1.Slave, DualNICBoundaryClock)),
			},
		},
	}
	Reset()
	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			GeneratePTPObjects(tt.args.mode)
			if got := GetFullDiscoveredConfig(tt.args.namespace, true); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetFullDiscoveredConfig() = %v, want %v", got, tt.want)
			}
			testclient.ClearTestClientsHolder()
		})
	}
}
func mockPtpConfig(name, namespace string, role ptpv1.PtpRole, mode PTPMode) *ptpv1.PtpConfig {
	// Label
	aLabel := pkg.PtpClockUnderTestNodeLabel
	// Match rule
	aMatchRule := ptpv1.MatchRule{}
	aMatchRule.NodeLabel = &aLabel
	// Ptp recommend
	aPtpRecommend := ptpv1.PtpRecommend{}
	aPtpRecommend.Match = append(aPtpRecommend.Match, aMatchRule)
	// Ptp config
	aConfig := ptpv1.PtpConfig{}
	aConfig.Name = name
	aConfig.Namespace = namespace
	aConfig.Spec.Recommend = []ptpv1.PtpRecommend{}
	aConfig.Spec.Recommend = append(aConfig.Spec.Recommend, aPtpRecommend)
	// ptp profile
	aProfile := ptpv1.PtpProfile{}
	aProfile.Name = &name
	if role == ptpv1.Master {
		aStringPtp4l := "-s"
		aProfile.Ptp4lOpts = &aStringPtp4l
		aStringPhc2sys := "-a -r -r -n 24"
		aProfile.Phc2sysOpts = &aStringPhc2sys
	} else {

		switch mode {
		case OrdinaryClock:
			aStringPtp4l := "-s -2"
			aProfile.Ptp4lOpts = &aStringPtp4l
			aStringPhc2sys := "-a -r -n 24"
			aProfile.Phc2sysOpts = &aStringPhc2sys
			aStringInterface := "eth0"
			aProfile.Interface = &aStringInterface
		case BoundaryClock:
			aStringPtp4l := "-2"
			aProfile.Ptp4lOpts = &aStringPtp4l
			aStringPhc2sys := "-a -r -n 24"
			aProfile.Phc2sysOpts = &aStringPhc2sys
			aStringInterface := "eth0"
			aProfile.Interface = &aStringInterface
			aString := generateBCConfig("eth0", "eth1", "eth2")
			aProfile.Ptp4lConf = &aString
		case DualNICBoundaryClock:
			aStringPtp4l := "-2"
			aProfile.Ptp4lOpts = &aStringPtp4l
			aStringInterface := "eth0"
			aProfile.Interface = &aStringInterface
			aString := generateBCConfig("eth0", "eth1", "eth2")
			aProfile.Ptp4lConf = &aString
			aProfile.Phc2sysOpts = nil
		case TelcoBoundaryClock:
			aStringPtp4l := "-2"
			aProfile.Ptp4lOpts = &aStringPtp4l
			aStringPhc2sys := "-a -r -n 24 -N 8 -R 16 -u 0"
			aProfile.Phc2sysOpts = &aStringPhc2sys
			aStringTs2phc := "-s generic"
			aProfile.Ts2PhcOpts = &aStringTs2phc
			aString := generateTelcoBCConfig("eth0", "eth1")
			aProfile.Ptp4lConf = &aString
			aStringTs2phcConf := generateTs2phcConfig("eth0")
			aProfile.Ts2PhcConf = &aStringTs2phcConf
			// Add mock plugins for TelcoBC
			aProfile.Plugins = mockTelcoBCPlugins("eth0")
		default:
		}
	}
	// ptp4l
	aConfig.Spec.Profile = append(aConfig.Spec.Profile, aProfile)

	return &aConfig
}

// Dynamic configuration generators for test mocks

func generateBCConfig(slaveIf, masterIf1, masterIf2 string) string {
	return fmt.Sprintf(`[%s]
masterOnly 0
[%s]
masterOnly 1
[%s]
masterOnly 1`, slaveIf, masterIf1, masterIf2)
}

func generateTelcoBCConfig(slaveIf, masterIf string) string {
	return fmt.Sprintf(`[%s]
masterOnly 0
[%s]
masterOnly 1
[global]
clock_type BC
boundary_clock_jbod 1
twoStepFlag 1
slaveOnly 0
priority1 128
priority2 128
domainNumber 24
clockClass 248`, slaveIf, masterIf)
}

func generateTs2phcConfig(interfaceName string) string {
	return fmt.Sprintf(`[global]
use_syslog  0
verbose 1
logging_level 7
ts2phc.pulsewidth 100000000
leapfile  /usr/share/zoneinfo/leap-seconds.list
[%s]
ts2phc.extts_polarity rising
ts2phc.extts_correction 0
ts2phc.master 0`, interfaceName)
}

func mockTelcoBCPlugins(interfaceName string) map[string]*apiextensions.JSON {
	// Create a simple mock plugin configuration for TelcoBC with dynamic interface
	pluginData := fmt.Sprintf(`{"enableDefaultConfig":false,"pins":{"%s":{"U.FL1":"0 1","U.FL2":"0 2"}}}`, interfaceName)
	plugins := make(map[string]*apiextensions.JSON)
	rawJSON := apiextensions.JSON{Raw: []byte(pluginData)}
	plugins["e810"] = &rawJSON
	return plugins
}

func mockNode(name string) *corev1.Node {
	aNode := corev1.Node{}
	aNode.Name = name
	aNode.Labels = make(map[string]string)
	aNode.Labels[pkg.PtpClockUnderTestNodeLabel] = ""
	return &aNode
}
func GeneratePTPObjects(mode PTPMode) {
	testclient.ClearTestClientsHolder()
	switch mode {
	case OrdinaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig(config1, namespace1, ptpv1.Slave, OrdinaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	case BoundaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig(config2, namespace1, ptpv1.Slave, BoundaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	case DualNICBoundaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig(config2, namespace1, ptpv1.Slave, BoundaryClock))
		mockClientObjects = append(mockClientObjects, mockPtpConfig(config3, namespace1, ptpv1.Slave, DualNICBoundaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	case TelcoBoundaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig(config1, namespace1, ptpv1.Slave, TelcoBoundaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	}
}

func TestStripPhc2sysRealtimeOpts(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "-a -r -n 24 -m -N 8 -R 16", want: "-a -n 24 -m -N 8 -R 16"},
		{in: "-a -r -r -n 24", want: "-a -n 24"},
		{in: "-a -n 24", want: "-a -n 24"},
		{in: "  -a   -r  -m  ", want: "-a -m"},
	}
	for _, tt := range tests {
		if got := stripPhc2sysRealtimeOpts(tt.in); got != tt.want {
			t.Fatalf("stripPhc2sysRealtimeOpts(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsTelcoGMMode(t *testing.T) {
	gm := map[PTPMode]bool{
		TelcoGrandMasterClock: true,
		TelcoGMOC:             true,
		TelcoGMBC:             true,
	}
	nonGM := []PTPMode{
		OrdinaryClock, BoundaryClock, DualNICBoundaryClock, DualNICBoundaryClockHA,
		DualFollowerClock, Discovery, TelcoBoundaryClock, None,
	}
	for mode, want := range gm {
		if got := isTelcoGMMode(mode); got != want {
			t.Errorf("isTelcoGMMode(%s) = %v, want %v", mode, got, want)
		}
	}
	for _, mode := range nonGM {
		if isTelcoGMMode(mode) {
			t.Errorf("isTelcoGMMode(%s) = true, want false", mode)
		}
	}
}

const (
	authNegativeTestSharedSAD   = "/etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf"
	authNegativeTestIsolatedSAD = "/etc/ptp-secret-mount/ptp-security-conf/auth-negative-test.conf"
)

func TestPrepareAuthNegativeSAD(t *testing.T) {
	input := `[security_association]
spp 0
allow_mutable 1
1 AES128 HEX:00

[security_association]
spp 1
seqid_window 20
1 AES128 HEX:11
2 SHA256-128 HEX:22
3 SHA256-128 HEX:33
`
	want := `[security_association]
spp 0
allow_mutable 1
1 AES128 HEX:00

[security_association]
spp 1
seqid_window 20
2 SHA256-128 HEX:22
3 SHA256-128 HEX:33
`

	got, err := PrepareAuthNegativeSAD(input)
	if err != nil {
		t.Fatalf("PrepareAuthNegativeSAD() error = %v", err)
	}
	if got != want {
		t.Fatalf("PrepareAuthNegativeSAD() = %q, want %q", got, want)
	}
}

func TestPrepareAuthNegativeSADRejectsMissingOrAmbiguousTargets(t *testing.T) {
	valid := `[security_association]
spp 1
1 AES128 HEX:11
2 SHA256-128 HEX:22
3 SHA256-128 HEX:33
`
	tests := []struct {
		name string
		sad  string
	}{
		{name: "missing key 1", sad: strings.Replace(valid, "1 AES128 HEX:11\n", "", 1)},
		{name: "missing key 2", sad: strings.Replace(valid, "2 SHA256-128 HEX:22\n", "", 1)},
		{name: "ambiguous SPP 1", sad: valid + valid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PrepareAuthNegativeSAD(tt.sad); err == nil {
				t.Fatal("PrepareAuthNegativeSAD() error = nil, want an error")
			}
		})
	}
}

func TestPrepareAuthNegativePtp4lConf(t *testing.T) {
	input := `[global]
sa_file /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf
spp -1
logging_level 6
[ens1f0]
spp 1
active_key_id 1
`
	want := `[global]
sa_file /etc/ptp-secret-mount/ptp-security-conf/auth-negative-test.conf
spp -1
logging_level 6
[ens1f0]
spp 1
active_key_id 2
`

	got, interfaces, err := PrepareAuthNegativePtp4lConf(input, authNegativeTestSharedSAD, authNegativeTestIsolatedSAD, "ens1f0")
	if err != nil {
		t.Fatalf("PrepareAuthNegativePtp4lConf() error = %v", err)
	}
	if got != want {
		t.Fatalf("PrepareAuthNegativePtp4lConf() = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(interfaces, []string{"ens1f0"}) {
		t.Fatalf("transmit interfaces = %v, want [ens1f0]", interfaces)
	}
}

func TestPrepareAuthNegativePtp4lConfRetainsTransmitKeyOnEverySPP1Port(t *testing.T) {
	input := `[global]
sa_file /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf
spp -1
logging_level 6
[ens1f0]
masterOnly 0
spp 1
active_key_id 1
[ens1f1]
masterOnly 1
spp 1
active_key_id 1
[ens2f0]
masterOnly 1
spp 0
active_key_id 3
`
	got, interfaces, err := PrepareAuthNegativePtp4lConf(input, authNegativeTestSharedSAD, authNegativeTestIsolatedSAD, "ens1f0")
	if err != nil {
		t.Fatalf("PrepareAuthNegativePtp4lConf() error = %v", err)
	}
	want := `[global]
sa_file /etc/ptp-secret-mount/ptp-security-conf/auth-negative-test.conf
spp -1
logging_level 6
[ens1f0]
masterOnly 0
spp 1
active_key_id 2
[ens1f1]
masterOnly 1
spp 1
active_key_id 2
[ens2f0]
masterOnly 1
spp 0
active_key_id 3
`
	if got != want {
		t.Fatalf("PrepareAuthNegativePtp4lConf() = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(interfaces, []string{"ens1f0", "ens1f1"}) {
		t.Fatalf("transmit interfaces = %v, want [ens1f0 ens1f1]", interfaces)
	}
}

func TestPrepareAuthNegativePtp4lConfRejectsInvalidInputs(t *testing.T) {
	base := `[global]
sa_file /etc/ptp-secret-mount/ptp-security-conf/ptp-security.conf
spp -1
logging_level 6
[ens1f0]
spp 1
active_key_id 1
`
	tests := []struct {
		name   string
		config string
		iface  string
	}{
		{name: "missing receive interface", config: base, iface: "ens9f0"},
		{name: "missing retained active key", config: strings.Replace(base, "active_key_id 1\n", "", 1), iface: "ens1f0"},
		{name: "nonstandard logging level", config: strings.Replace(base, "logging_level 6", "logging_level 7", 1), iface: "ens1f0"},
		{name: "unexpected global sa_file", config: strings.Replace(base, "ptp-security.conf\n", "other.conf\n", 1), iface: "ens1f0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := PrepareAuthNegativePtp4lConf(tt.config, authNegativeTestSharedSAD, authNegativeTestIsolatedSAD, tt.iface); err == nil {
				t.Fatal("PrepareAuthNegativePtp4lConf() error = nil, want an error")
			}
		})
	}
}
