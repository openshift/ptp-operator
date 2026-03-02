package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }

func makePtpConfigList(configs ...ptpv1.PtpConfig) *ptpv1.PtpConfigList {
	return &ptpv1.PtpConfigList{Items: configs}
}

func makePtpConfig(name string, profiles []ptpv1.PtpProfile, recommends []ptpv1.PtpRecommend) ptpv1.PtpConfig {
	return ptpv1.PtpConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "openshift-ptp"},
		Spec:       ptpv1.PtpConfigSpec{Profile: profiles, Recommend: recommends},
	}
}

func makeProfile(name string, settings map[string]string) ptpv1.PtpProfile {
	return ptpv1.PtpProfile{Name: strPtr(name), PtpSettings: settings}
}

func makeRecommend(profile string, priority int64, nodeLabel string) ptpv1.PtpRecommend {
	return ptpv1.PtpRecommend{
		Profile:  strPtr(profile),
		Priority: int64Ptr(priority),
		Match:    []ptpv1.MatchRule{{NodeLabel: strPtr(nodeLabel)}},
	}
}

func makeNode(name string, labels map[string]string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
}

func TestQualifyProfileName(t *testing.T) {
	tests := []struct {
		crName, profileName, expected string
	}{
		{"tbc", "maestro", "tbc_maestro"},
		{"T-BC", "01-tbc-tr", "T-BC_01-tbc-tr"},
		{"config-alpha", "my-profile", "config-alpha_my-profile"},
	}
	for _, tt := range tests {
		result := qualifyProfileName(tt.crName, tt.profileName)
		assert.Equal(t, tt.expected, result, "qualifyProfileName(%s, %s)", tt.crName, tt.profileName)
	}
}

func TestProfileExistsInCR(t *testing.T) {
	list := makePtpConfigList(
		makePtpConfig("alpha", []ptpv1.PtpProfile{makeProfile("maestro", nil)}, nil),
		makePtpConfig("beta", []ptpv1.PtpProfile{makeProfile("maestro", nil)}, nil),
	)

	assert.True(t, profileExistsInCR("alpha", "maestro", list), "profile exists in alpha")
	assert.True(t, profileExistsInCR("beta", "maestro", list), "profile exists in beta")
	assert.False(t, profileExistsInCR("alpha", "nonexistent", list), "profile does not exist")
	assert.False(t, profileExistsInCR("gamma", "maestro", list), "CR does not exist")
}

func TestProfileExistsInCR_NilProfile(t *testing.T) {
	list := makePtpConfigList(
		ptpv1.PtpConfig{ObjectMeta: metav1.ObjectMeta{Name: "empty"}, Spec: ptpv1.PtpConfigSpec{}},
	)
	assert.False(t, profileExistsInCR("empty", "anything", list))
}

func TestResolveProfileReference_UserQualified(t *testing.T) {
	// Case B: user already qualified "alpha_maestro" and alpha CR has profile "maestro"
	list := makePtpConfigList(
		makePtpConfig("alpha", []ptpv1.PtpProfile{makeProfile("maestro", nil)}, nil),
		makePtpConfig("beta", []ptpv1.PtpProfile{makeProfile("other", nil)}, nil),
	)
	cfg := list.Items[1] // beta is the current CR

	result := resolveProfileReference("alpha_maestro", "controllingProfile", &cfg, list)
	assert.Equal(t, "alpha_maestro", result, "user-qualified reference should be left as-is")
	assert.Empty(t, cfg.Status.Conditions, "no condition should be set for valid reference")
}

func TestResolveProfileReference_UnderscoreInProfileName_NotQualified(t *testing.T) {
	// Case A: profile named "tbc_tr_profile" in CR "tbc"
	// CR "tbc2" references "tbc_tr_profile" as controllingProfile
	// "tbc" is a valid CR name, but "tr_profile" is NOT a profile in "tbc"
	// so it's not user-qualified -- should search full name and find it in "tbc"
	list := makePtpConfigList(
		makePtpConfig("tbc", []ptpv1.PtpProfile{makeProfile("tbc_tr_profile", nil)}, nil),
		makePtpConfig("tbc2", []ptpv1.PtpProfile{makeProfile("controlled", nil)}, nil),
	)
	cfg := list.Items[1]

	result := resolveProfileReference("tbc_tr_profile", "controllingProfile", &cfg, list)
	assert.Equal(t, "tbc_tbc_tr_profile", result, "should qualify with owning CR name")
}

func TestResolveProfileReference_SimpleUnqualified(t *testing.T) {
	// Case C: simple profile name with no underscore
	list := makePtpConfigList(
		makePtpConfig("config-a", []ptpv1.PtpProfile{makeProfile("bc-profile1", nil)}, nil),
	)
	cfg := list.Items[0]

	result := resolveProfileReference("bc-profile1", "controllingProfile", &cfg, list)
	assert.Equal(t, "config-a_bc-profile1", result)
}

func TestResolveProfileReference_CrossCR(t *testing.T) {
	// Case D: profile exists in a different CR
	list := makePtpConfigList(
		makePtpConfig("alpha", []ptpv1.PtpProfile{makeProfile("bc1", nil)}, nil),
		makePtpConfig("beta", []ptpv1.PtpProfile{makeProfile("bc2", nil)}, nil),
	)
	cfg := list.Items[0] // alpha

	result := resolveProfileReference("bc2", "haProfiles", &cfg, list)
	assert.Equal(t, "beta_bc2", result, "should resolve to the CR that owns bc2")
}

func TestResolveProfileReference_Unresolved(t *testing.T) {
	// Case E: profile doesn't exist anywhere
	list := makePtpConfigList(
		makePtpConfig("alpha", []ptpv1.PtpProfile{makeProfile("bc1", nil)}, nil),
	)
	cfg := list.Items[0]

	result := resolveProfileReference("nonexistent", "controllingProfile", &cfg, list)
	assert.Equal(t, "nonexistent", result, "unresolved should return original value")
	assert.Len(t, cfg.Status.Conditions, 1, "should set a condition")
	assert.Equal(t, "ProfileReferenceValid", cfg.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionFalse, cfg.Status.Conditions[0].Status)
	assert.Equal(t, "UnresolvedProfileReference", cfg.Status.Conditions[0].Reason)
}

func TestResolveProfileReference_UnderscoreNoCRMatch(t *testing.T) {
	// Case F: profile name "my_custom_profile", no CR named "my"
	list := makePtpConfigList(
		makePtpConfig("config-x", []ptpv1.PtpProfile{makeProfile("my_custom_profile", nil)}, nil),
	)
	cfg := list.Items[0]

	result := resolveProfileReference("my_custom_profile", "controllingProfile", &cfg, list)
	assert.Equal(t, "config-x_my_custom_profile", result, "no CR named 'my', so search full name and qualify")
}

func TestQualifyCrossProfileReferences_HAProfiles(t *testing.T) {
	// Case I: HA profiles from different CRs
	list := makePtpConfigList(
		makePtpConfig("alpha", []ptpv1.PtpProfile{makeProfile("bc1", nil)}, nil),
		makePtpConfig("beta", []ptpv1.PtpProfile{makeProfile("bc2", nil)}, nil),
	)
	cfg := list.Items[0]
	settings := map[string]string{"haProfiles": "bc1, bc2"}

	qualifyCrossProfileReferences(settings, &cfg, list)

	assert.Equal(t, "alpha_bc1,beta_bc2", settings["haProfiles"])
}

func TestQualifyCrossProfileReferences_ControllingProfile(t *testing.T) {
	// Case J: controllingProfile already user-qualified
	list := makePtpConfigList(
		makePtpConfig("other-cr", []ptpv1.PtpProfile{makeProfile("some-profile", nil)}, nil),
		makePtpConfig("this-cr", []ptpv1.PtpProfile{
			makeProfile("controlled", map[string]string{"controllingProfile": "other-cr_some-profile"}),
		}, nil),
	)
	cfg := list.Items[1]
	settings := cfg.Spec.Profile[0].PtpSettings

	qualifyCrossProfileReferences(settings, &cfg, list)

	assert.Equal(t, "other-cr_some-profile", settings["controllingProfile"], "already qualified, should be unchanged")
}

func TestGetRecommendProfiles_Collision(t *testing.T) {
	// Case G: two CRs define "maestro" at same priority, both match same node
	node := makeNode("worker-1", map[string]string{"node-role.kubernetes.io/worker": ""})
	list := makePtpConfigList(
		makePtpConfig("config-alpha", []ptpv1.PtpProfile{makeProfile("maestro", nil)},
			[]ptpv1.PtpRecommend{makeRecommend("maestro", 10, "node-role.kubernetes.io/worker")}),
		makePtpConfig("config-beta", []ptpv1.PtpProfile{makeProfile("maestro", nil)},
			[]ptpv1.PtpRecommend{makeRecommend("maestro", 10, "node-role.kubernetes.io/worker")}),
	)

	profiles, err := getRecommendProfiles(list, node)
	assert.NoError(t, err)
	assert.Len(t, profiles, 2, "should have two distinct profiles")

	names := []string{*profiles[0].Name, *profiles[1].Name}
	assert.Contains(t, names, "config-alpha_maestro")
	assert.Contains(t, names, "config-beta_maestro")
}

func TestGetRecommendProfiles_OriginalNamePreserved(t *testing.T) {
	// Case H: getRecommendNodePtpProfilesForConfig returns original names (for status.matchList)
	node := makeNode("worker-1", map[string]string{"ptp/test": ""})
	cfg := makePtpConfig("my-config", []ptpv1.PtpProfile{makeProfile("my-profile", nil)},
		[]ptpv1.PtpRecommend{makeRecommend("my-profile", 5, "ptp/test")})

	profiles, err := getRecommendNodePtpProfilesForConfig(&cfg, node)
	assert.NoError(t, err)
	assert.Len(t, profiles, 1)
	assert.Equal(t, "my-profile", *profiles[0].Name, "original name should be preserved for status.matchList")
}

func TestGetRecommendProfiles_CrossCR_ControllingProfile(t *testing.T) {
	// T-BC scenario: two profiles in same CR, one controls the other
	node := makeNode("worker-1", map[string]string{"ptp/tbc": ""})
	list := makePtpConfigList(
		makePtpConfig("tbc-config", []ptpv1.PtpProfile{
			makeProfile("01-tbc-tt", map[string]string{"controllingProfile": "01-tbc-tr"}),
			makeProfile("01-tbc-tr", nil),
		}, []ptpv1.PtpRecommend{
			makeRecommend("01-tbc-tt", 5, "ptp/tbc"),
			makeRecommend("01-tbc-tr", 5, "ptp/tbc"),
		}),
	)

	profiles, err := getRecommendProfiles(list, node)
	assert.NoError(t, err)
	assert.Len(t, profiles, 2)

	for _, p := range profiles {
		if *p.Name == "tbc-config_01-tbc-tt" {
			assert.Equal(t, "tbc-config_01-tbc-tr", p.PtpSettings["controllingProfile"],
				"controllingProfile should be qualified with the same CR name")
		}
	}
}

func TestGetRecommendProfiles_CrossCR_HAProfiles(t *testing.T) {
	// DualNIC BC HA: two CRs, each with one profile, phc2sys references both
	node := makeNode("worker-1", map[string]string{"ptp/ha": ""})
	list := makePtpConfigList(
		makePtpConfig("bc-primary", []ptpv1.PtpProfile{makeProfile("bc1", nil)},
			[]ptpv1.PtpRecommend{makeRecommend("bc1", 5, "ptp/ha")}),
		makePtpConfig("bc-secondary", []ptpv1.PtpProfile{makeProfile("bc2", nil)},
			[]ptpv1.PtpRecommend{makeRecommend("bc2", 5, "ptp/ha")}),
		makePtpConfig("phc2sys-config", []ptpv1.PtpProfile{
			makeProfile("phc2sys-ha", map[string]string{"haProfiles": "bc1,bc2"}),
		}, []ptpv1.PtpRecommend{makeRecommend("phc2sys-ha", 5, "ptp/ha")}),
	)

	profiles, err := getRecommendProfiles(list, node)
	assert.NoError(t, err)

	for _, p := range profiles {
		if *p.Name == "phc2sys-config_phc2sys-ha" {
			assert.Equal(t, "bc-primary_bc1,bc-secondary_bc2", p.PtpSettings["haProfiles"],
				"haProfiles should be qualified with correct owning CR names")
		}
	}
}
