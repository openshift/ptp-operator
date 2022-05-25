package testconfig

import (
	"os"
	"reflect"
	"testing"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	testclient "github.com/openshift/ptp-operator/test/utils/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
				ptpDiscoveryRes{},
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
			args: args{namespace: "namespace1",
				mode: OrdinaryClock},
			want: TestConfig{
				PtpModeDesired:            None,
				PtpModeDiscovered:         OrdinaryClock,
				Status:                    DiscoverySuccessStatus,
				DiscoveredSlavePtpConfig:  ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config2", "namespace1", ptpv1.Slave, OrdinaryClock)},
				DiscoveredMasterPtpConfig: ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config1", "namespace1", ptpv1.Master, OrdinaryClock)},
			},
		},
		{
			name: "Boundary clock",
			args: args{namespace: "namespace1",
				mode: BoundaryClock},
			want: TestConfig{
				PtpModeDesired:            None,
				PtpModeDiscovered:         BoundaryClock,
				Status:                    DiscoverySuccessStatus,
				DiscoveredSlavePtpConfig:  ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config2", "namespace1", ptpv1.Slave, BoundaryClock)},
				DiscoveredMasterPtpConfig: ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config1", "namespace1", ptpv1.Master, OrdinaryClock)},
			},
		},
		{
			name: "Dual NIC Boundary clock",
			args: args{namespace: "namespace1",
				mode: DualNICBoundaryClock},
			want: TestConfig{
				PtpModeDesired:                    None,
				PtpModeDiscovered:                 DualNICBoundaryClock,
				Status:                            DiscoverySuccessStatus,
				DiscoveredSlavePtpConfig:          ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config2", "namespace1", ptpv1.Slave, BoundaryClock)},
				DiscoveredSlavePtpConfigSecondary: ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config3", "namespace1", ptpv1.Slave, DualNICBoundaryClock)},
				DiscoveredMasterPtpConfig:         ptpDiscoveryRes{Label: "label1", Config: *mockPtpConfig("config1", "namespace1", ptpv1.Master, OrdinaryClock)},
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
	aLabel := "label1"
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
	if role == ptpv1.Master {
		aStringPtp4l := "-s"
		aProfile.Ptp4lOpts = &aStringPtp4l
		aStringPhc2sys := "-a -r -r"
		aProfile.Phc2sysOpts = &aStringPhc2sys

	} else {
		aStringPtp4l := "-s"
		aProfile.Ptp4lOpts = &aStringPtp4l
		aStringPhc2sys := "-a -r"
		aProfile.Phc2sysOpts = &aStringPhc2sys
		aStringInterface := "eth0"
		aProfile.Interface = &aStringInterface
		switch mode {
		case BoundaryClock:
			aString := ptp4lconfBc
			aProfile.Ptp4lConf = &aString
		case DualNICBoundaryClock:
			aString := ptp4lconfBc
			aProfile.Ptp4lConf = &aString
			aProfile.Phc2sysOpts = nil
		default:
		}
	}
	// ptp4l
	aConfig.Spec.Profile = append(aConfig.Spec.Profile, aProfile)

	return &aConfig
}

const ptp4lconfBc = `[ens7f0]
masterOnly 0
[ens7f1]
masterOnly 1
[ens7f2]
masterOnly 1`

func mockNode(name string) *corev1.Node {
	aNode := corev1.Node{}
	aNode.Name = name
	aNode.Labels = make(map[string]string)
	aNode.Labels["label1"] = ""
	return &aNode
}
func GeneratePTPObjects(mode PTPMode) {
	testclient.ClearTestClientsHolder()
	switch mode {
	case OrdinaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config1", "namespace1", ptpv1.Master, OrdinaryClock))
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config2", "namespace1", ptpv1.Slave, OrdinaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	case BoundaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config1", "namespace1", ptpv1.Master, OrdinaryClock))
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config2", "namespace1", ptpv1.Slave, BoundaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	case DualNICBoundaryClock:
		var mockClientObjects []runtime.Object
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config1", "namespace1", ptpv1.Master, OrdinaryClock))
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config2", "namespace1", ptpv1.Slave, BoundaryClock))
		mockClientObjects = append(mockClientObjects, mockPtpConfig("config3", "namespace1", ptpv1.Slave, DualNICBoundaryClock))
		mockClientObjects = append(mockClientObjects, mockNode("node1"))
		_ = testclient.GetTestClientSet(mockClientObjects)
	}
}
