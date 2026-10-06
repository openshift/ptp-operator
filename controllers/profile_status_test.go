package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptpv2alpha1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v2alpha1"
)

func phc2sysPtr(s string) *string { return &s }

func profileWithPhc2sys(name, phc2sys string, settings map[string]string) ptpv1.PtpProfile {
	p := makeProfile(name, settings)
	if phc2sys != "" {
		p.Phc2sysOpts = phc2sysPtr(phc2sys)
	}
	return p
}

func nodeMatch(node, profile string) ptpv1.NodeMatchList {
	return ptpv1.NodeMatchList{NodeName: strPtr(node), Profile: strPtr(profile)}
}

func makeDevice(node string, profiles ...ptpv1.NodeProfileStatus) ptpv1.NodePtpDevice {
	return ptpv1.NodePtpDevice{
		ObjectMeta: metav1.ObjectMeta{Name: node, Namespace: "openshift-ptp"},
		Status: ptpv1.NodePtpDeviceStatus{
			Sync: &ptpv1.SyncStatus{Profiles: profiles},
		},
	}
}

func makeDeviceList(devices ...ptpv1.NodePtpDevice) *ptpv1.NodePtpDeviceList {
	return &ptpv1.NodePtpDeviceList{Items: devices}
}

func statusByName(t *testing.T, statuses []ptpv1.ProfileStatus, name string) ptpv1.ProfileStatus {
	t.Helper()
	for _, s := range statuses {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("profile status %q not found", name)
	return ptpv1.ProfileStatus{}
}

func TestBuildProfileStatuses_TBC(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		profileWithPhc2sys("tbc-tr", "-s", nil),
		profileWithPhc2sys("tbc-tt", "", map[string]string{"controllingProfile": "tbc-tr"}),
	}, nil)
	// Both profiles are T-BC; the (tr)/(tt) suffixes are test-only so we can
	// check clockType is copied per profile and not swapped.
	devices := makeDeviceList(makeDevice("worker-1",
		ptpv1.NodeProfileStatus{Name: "tbc-config_tbc-tr", ClockType: "T-BC(tr)"},
		ptpv1.NodeProfileStatus{Name: "tbc-config_tbc-tt", ClockType: "T-BC(tt)"},
	))

	statuses := buildProfileStatuses(&cfg, devices, nil, nil)
	assert.Len(t, statuses, 2)

	tr := statusByName(t, statuses, "tbc-config_tbc-tr")
	assert.Equal(t, []string{"tbc-config_tbc-tt"}, tr.Controls)
	assert.True(t, tr.HasPhc2sys)
	assert.Equal(t, []string{"worker-1"}, tr.AppliedOnNodes)
	assert.Equal(t, "T-BC(tr)", tr.ClockType)
	assert.Empty(t, tr.ControlledBy)

	tt := statusByName(t, statuses, "tbc-config_tbc-tt")
	assert.Equal(t, "tbc-config_tbc-tr", tt.ControlledBy)
	assert.False(t, tt.HasPhc2sys)
	assert.Equal(t, []string{"worker-1"}, tt.AppliedOnNodes)
	assert.Equal(t, "T-BC(tt)", tt.ClockType)
	assert.Empty(t, tt.Controls)
}

func TestBuildProfileStatuses_DualNICBCHA(t *testing.T) {
	cfg := makePtpConfig("dualnic-bc-ha", []ptpv1.PtpProfile{
		makeProfile("bc-primary", nil),
		makeProfile("bc-secondary", nil),
		profileWithPhc2sys("ha-phc2sys", "-a -r", map[string]string{"haProfiles": "bc-primary,bc-secondary"}),
	}, nil)

	statuses := buildProfileStatuses(&cfg, nil, nil, nil)
	assert.Len(t, statuses, 3)

	primary := statusByName(t, statuses, "dualnic-bc-ha_bc-primary")
	assert.Equal(t, "dualnic-bc-ha_ha-phc2sys", primary.PartOfHa)
	assert.False(t, primary.HasPhc2sys)

	secondary := statusByName(t, statuses, "dualnic-bc-ha_bc-secondary")
	assert.Equal(t, "dualnic-bc-ha_ha-phc2sys", secondary.PartOfHa)
	assert.False(t, secondary.HasPhc2sys)

	ha := statusByName(t, statuses, "dualnic-bc-ha_ha-phc2sys")
	assert.Equal(t, []string{"dualnic-bc-ha_bc-primary", "dualnic-bc-ha_bc-secondary"}, ha.HaMembers)
	assert.True(t, ha.HasPhc2sys)
	assert.Empty(t, ha.PartOfHa)
}

func TestBuildProfileStatuses_QualifiedControllingProfile(t *testing.T) {
	cfg := makePtpConfig("t-bc", []ptpv1.PtpProfile{
		makeProfile("tbc-tr", nil),
		makeProfile("tbc-tt", map[string]string{"controllingProfile": "t-bc_tbc-tr"}),
	}, nil)

	statuses := buildProfileStatuses(&cfg, nil, nil, nil)
	tr := statusByName(t, statuses, "t-bc_tbc-tr")
	assert.Equal(t, []string{"t-bc_tbc-tt"}, tr.Controls)
	tt := statusByName(t, statuses, "t-bc_tbc-tt")
	assert.Equal(t, "t-bc_tbc-tr", tt.ControlledBy)
}

func TestDetectWarnings_MultiplePhc2sys(t *testing.T) {
	cfg := makePtpConfig("conflict", []ptpv1.PtpProfile{
		profileWithPhc2sys("profile-a", "-s", nil),
		profileWithPhc2sys("profile-b", "-s", nil),
	}, nil)
	matchList := []ptpv1.NodeMatchList{
		nodeMatch("worker-1", "profile-a"),
		nodeMatch("worker-1", "profile-b"),
	}

	warnings := detectWarnings(&cfg, matchList, nil)
	assert.Equal(t, []string{"Multiple profiles have phc2sys enabled on worker-1: conflict_profile-a, conflict_profile-b"}, warnings)
}

func TestDetectWarnings_MultiplePhc2sysCrossCR(t *testing.T) {
	gm := makePtpConfig("gm-config", []ptpv1.PtpProfile{
		profileWithPhc2sys("grandmaster", "-s", nil),
	}, nil)
	tbc := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		profileWithPhc2sys("tbc-tr", "-s", nil),
	}, nil)
	gm.Status.MatchList = []ptpv1.NodeMatchList{nodeMatch("worker-1", "gm-config_grandmaster")}
	tbc.Status.MatchList = []ptpv1.NodeMatchList{nodeMatch("worker-1", "tbc-config_tbc-tr")}
	list := makePtpConfigList(gm, tbc)

	warnings := detectWarnings(&tbc, tbc.Status.MatchList, list)
	assert.Equal(t, []string{"Multiple profiles have phc2sys enabled on worker-1: gm-config_grandmaster, tbc-config_tbc-tr"}, warnings)
}

func TestDetectWarnings_ControlledProfileNotApplied(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		profileWithPhc2sys("tbc-tr", "-s", nil),
		makeProfile("tbc-tt", map[string]string{"controllingProfile": "tbc-tr"}),
	}, nil)
	matchList := []ptpv1.NodeMatchList{
		nodeMatch("worker-1", "tbc-tr"),
	}

	warnings := detectWarnings(&cfg, matchList, nil)
	assert.Equal(t, []string{"Profile tbc-config_tbc-tt is controlled by tbc-config_tbc-tr but not applied on any node"}, warnings)
}

func TestDetectWarnings_NoWarningWhenControllerAlsoMissing(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		makeProfile("tbc-tr", nil),
		makeProfile("tbc-tt", map[string]string{"controllingProfile": "tbc-tr"}),
	}, nil)

	assert.Empty(t, detectWarnings(&cfg, nil, nil))
}

func TestProfileReferencesValid(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		makeProfile("tbc-tr", nil),
		makeProfile("tbc-tt", map[string]string{"controllingProfile": "tbc-tr"}),
	}, nil)
	list := makePtpConfigList(cfg)

	valid, msg := profileReferencesValid(&cfg, list)
	assert.True(t, valid)
	assert.Equal(t, "All profile references are valid", msg)
}

func TestProfileReferencesValid_Unresolved(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		makeProfile("tbc-tt", map[string]string{"controllingProfile": "missing-tr"}),
	}, nil)
	list := makePtpConfigList(cfg)

	valid, msg := profileReferencesValid(&cfg, list)
	assert.False(t, valid)
	assert.Contains(t, msg, "missing-tr")
}

func TestProfileReferencesValid_UnresolvedHaMember(t *testing.T) {
	cfg := makePtpConfig("ha", []ptpv1.PtpProfile{
		makeProfile("ha-phc2sys", map[string]string{"haProfiles": "missing"}),
	}, nil)
	list := makePtpConfigList(cfg)

	valid, msg := profileReferencesValid(&cfg, list)
	assert.False(t, valid)
	assert.Contains(t, msg, "missing")
}

func TestBuildProfileStatuses_MatchListDoesNotFillAppliedOnNodes(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		profileWithPhc2sys("tbc-tr", "-s", nil),
	}, nil)

	statuses := buildProfileStatuses(&cfg, nil, nil, nil)
	tr := statusByName(t, statuses, "tbc-config_tbc-tr")
	assert.Empty(t, tr.AppliedOnNodes)
}

func TestBuildProfileStatuses_ClockTypeFromNodePtpDeviceNotSpec(t *testing.T) {
	ocConf := "[global]\nslaveOnly 1\n[ens1f0]\n"
	oc := makeProfile("slave", nil)
	oc.Ptp4lConf = strPtr(ocConf)
	tbc := makeProfile("tbc-tr", map[string]string{"clockType": "T-BC"})
	cfg := makePtpConfig("mixed", []ptpv1.PtpProfile{oc, tbc}, nil)

	statuses := buildProfileStatuses(&cfg, nil, nil, nil)
	assert.Empty(t, statusByName(t, statuses, "mixed_slave").ClockType)
	assert.Empty(t, statusByName(t, statuses, "mixed_tbc-tr").ClockType)
}

func TestBuildProfileStatuses_ClockTypesFromDevice(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		makeProfile("tbc-tr", map[string]string{"clockType": "T-BC"}),
		makeProfile("slave", nil),
	}, nil)
	devices := makeDeviceList(makeDevice("worker-1",
		ptpv1.NodeProfileStatus{Name: "tbc-config_tbc-tr", ClockType: "T-BC"},
		ptpv1.NodeProfileStatus{Name: "tbc-config_slave", ClockType: "OC"},
	))

	statuses := buildProfileStatuses(&cfg, devices, nil, nil)
	tr := statusByName(t, statuses, "tbc-config_tbc-tr")
	assert.Equal(t, "T-BC", tr.ClockType)
	slave := statusByName(t, statuses, "tbc-config_slave")
	assert.Equal(t, "OC", slave.ClockType)
}

func TestAppliedForProfile_MultipleNodes(t *testing.T) {
	cfg := makePtpConfig("slave-config", []ptpv1.PtpProfile{
		makeProfile("slave", nil),
	}, nil)
	devices := makeDeviceList(
		makeDevice("worker-1", ptpv1.NodeProfileStatus{Name: "slave-config_slave", ClockType: "OC"}),
		makeDevice("worker-2", ptpv1.NodeProfileStatus{Name: "slave-config_slave", ClockType: "OC"}),
	)

	statuses := buildProfileStatuses(&cfg, devices, nil, nil)
	slave := statusByName(t, statuses, "slave-config_slave")
	assert.Equal(t, []string{"worker-1", "worker-2"}, slave.AppliedOnNodes)
	assert.Equal(t, "OC", slave.ClockType)
}

func TestBuildProfileStatuses_HardwareConfigs(t *testing.T) {
	cfg := makePtpConfig("tbc-config", []ptpv1.PtpProfile{
		makeProfile("tbc-tr", nil),
		makeProfile("tbc-tt", map[string]string{"controllingProfile": "tbc-tr"}),
	}, nil)
	hwList := &ptpv2alpha1.HardwareConfigList{Items: []ptpv2alpha1.HardwareConfig{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "tbc-nic", Namespace: "openshift-ptp"},
			Spec:       ptpv2alpha1.HardwareConfigSpec{RelatedPtpProfileName: "tbc-tr"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "tbc-nic-qualified", Namespace: "openshift-ptp"},
			Spec:       ptpv2alpha1.HardwareConfigSpec{RelatedPtpProfileName: "tbc-config_tbc-tr"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "other-nic", Namespace: "openshift-ptp"},
			Spec:       ptpv2alpha1.HardwareConfigSpec{RelatedPtpProfileName: "tbc-tt"},
		},
	}}

	statuses := buildProfileStatuses(&cfg, nil, nil, hwList)
	tr := statusByName(t, statuses, "tbc-config_tbc-tr")
	assert.Equal(t, []string{"tbc-nic", "tbc-nic-qualified"}, tr.HardwareConfigs)
	tt := statusByName(t, statuses, "tbc-config_tbc-tt")
	assert.Equal(t, []string{"other-nic"}, tt.HardwareConfigs)
}
