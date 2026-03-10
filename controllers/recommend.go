package controllers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

const ProfileNameSeperator = "_"

// create a node-unique profile name by prepending the PtpConfig CR name
func qualifyProfileName(crName, profileName string) string {
	return crName + ProfileNameSeperator + profileName
}

// check if a profile with the given name exists in the specified CR
func profileExistsInCR(crName, profileName string, ptpConfigList *ptpv1.PtpConfigList) bool {
	for _, cfg := range ptpConfigList.Items {
		if cfg.Name != crName || cfg.Spec.Profile == nil {
			continue
		}
		for _, p := range cfg.Spec.Profile {
			if p.Name != nil && *p.Name == profileName {
				return true
			}
		}
	}
	return false
}

// check if the user has already added a valid prefix to the profile name, if not add a prefix
func resolveProfileReference(value, settingName string, ptpConfig *ptpv1.PtpConfig, ptpConfigList *ptpv1.PtpConfigList) string {
	// check if the user already added a valid prefix to the profile name
	if parts := strings.SplitN(value, "_", 2); len(parts) == 2 {
		if profileExistsInCR(parts[0], parts[1], ptpConfigList) {
			return value
		}
	}

	// search for the profile by its full name across all CRs if the user did not add a valid prefix
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Profile == nil {
			continue
		}
		for _, p := range cfg.Spec.Profile {
			if p.Name != nil && *p.Name == value {
				return qualifyProfileName(cfg.Name, value)
			}
		}
	}

	// profile not found anywhere -- warn and set condition on the PtpConfig
	msg := fmt.Sprintf("profile '%s' referenced in %s not found in any PtpConfig CR", value, settingName)
	glog.Warningf("PtpConfig %s: %s", ptpConfig.Name, msg)
	meta.SetStatusCondition(&ptpConfig.Status.Conditions, metav1.Condition{
		Type:               "ProfileReferenceValid",
		Status:             metav1.ConditionFalse,
		Reason:             "UnresolvedProfileReference",
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	})
	return value
}

// update controllingProfile and haProfiles settings with qualified names
func qualifyCrossProfileReferences(settings map[string]string, ptpConfig *ptpv1.PtpConfig, ptpConfigList *ptpv1.PtpConfigList) {
	if cp, ok := settings["controllingProfile"]; ok && cp != "" {
		settings["controllingProfile"] = resolveProfileReference(cp, "controllingProfile", ptpConfig, ptpConfigList)
	}
	if ha, ok := settings["haProfiles"]; ok && ha != "" {
		parts := strings.Split(ha, ",")
		for i, p := range parts {
			parts[i] = resolveProfileReference(strings.TrimSpace(p), "haProfiles", ptpConfig, ptpConfigList)
		}
		settings["haProfiles"] = strings.Join(parts, ",")
	}
}

func printWhenNotNil(p interface{}, description string) {
	switch v := p.(type) {
	case *string:
		if v != nil {
			glog.Info(description, ": ", *v)
		}
	case *int64:
		if v != nil {
			glog.Info(description, ": ", *v)
		}
	default:
		glog.Info(description, ": ", v)
	}
}

// getRecommendNodePtpProfiles return recommended node ptp profile
func getRecommendNodePtpProfiles(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	glog.V(2).Infof("in getRecommendNodePtpProfiles")

	profiles, err := getRecommendProfiles(ptpConfigList, node)
	if err != nil {
		return nil, fmt.Errorf("get recommended ptp profiles failed: %v", err)
	}

	glog.Infof("ptp profiles to be updated for node: %s", node.Name)
	for _, profile := range profiles {
		glog.Infof("------------------------------------")
		if profile.Name != nil {
			if parts := strings.SplitN(*profile.Name, "_", 2); len(parts) == 2 {
				glog.Infof("PtpConfig CR: %s, Profile: %s", parts[0], parts[1])
			} else {
				printWhenNotNil(profile.Name, "Profile Name")
			}
		}
		printWhenNotNil(profile.Interface, "Interface")
		printWhenNotNil(profile.Ptp4lOpts, "Ptp4lOpts")
		printWhenNotNil(profile.Phc2sysOpts, "Phc2sysOpts")
		printWhenNotNil(profile.Ptp4lConf, "Ptp4lConf")
		printWhenNotNil(profile.PtpSchedulingPolicy, "PtpSchedulingPolicy")
		printWhenNotNil(profile.PtpSchedulingPriority, "PtpSchedulingPriority")
		glog.Infof("------------------------------------")
	}

	return profiles, nil
}

func getRecommendProfiles(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	glog.V(2).Infof("In getRecommendProfiles")

	profilesNames := getRecommendProfilesNames(ptpConfigList, node)
	glog.V(2).Infof("recommended ptp profiles names are %v for node: %s", returnMapKeys(profilesNames), node.Name)

	profiles := []ptpv1.PtpProfile{}
	foundNames := make(map[string]bool)
	for i := range ptpConfigList.Items {
		cfg := &ptpConfigList.Items[i]
		if cfg.Spec.Profile == nil {
			continue
		}
		for _, profile := range cfg.Spec.Profile {
			if profile.Name == nil {
				continue
			}
			if _, exist := profilesNames[*profile.Name]; !exist {
				continue
			}
			foundNames[*profile.Name] = true
			profileCopy := profile.DeepCopy()
			qualifiedName := qualifyProfileName(cfg.Name, *profile.Name)
			profileCopy.Name = &qualifiedName

			if profileCopy.PtpSettings != nil {
				qualifyCrossProfileReferences(profileCopy.PtpSettings, cfg, ptpConfigList)
			}

			profiles = append(profiles, *profileCopy)
		}
	}

	if len(foundNames) != len(profilesNames) {
		return nil, fmt.Errorf("Failed to find all the recommended profiles")
	}
	// sort profiles by name
	sort.SliceStable(profiles, func(i, j int) bool {
		return *profiles[i].Name < *profiles[j].Name
	})

	return profiles, nil
}

func getRecommendProfilesNames(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) map[string]interface{} {
	glog.V(2).Infof("In getRecommendProfilesNames")

	var (
		allRecommend []ptpv1.PtpRecommend
	)

	// append recommend section from each custom resource into one list
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Recommend != nil {
			allRecommend = append(allRecommend, cfg.Spec.Recommend...)
		}
	}

	// allRecommend sorted by priority
	// priority 0 will become the first item in allRecommend
	sort.Slice(allRecommend, func(i, j int) bool {
		if allRecommend[i].Priority != nil && allRecommend[j].Priority != nil {
			return *allRecommend[i].Priority < *allRecommend[j].Priority
		}
		return allRecommend[i].Priority != nil
	})

	// Add all the profiles with the same priority
	profilesNames := make(map[string]interface{})
	foundPolicy := false
	priority := int64(-1)

	// loop allRecommend from high priority(0) to low(*)
	for _, r := range allRecommend {

		// ignore if profile not define in recommend
		if r.Profile == nil {
			continue
		}

		// ignore if match section is empty
		if len(r.Match) == 0 {
			continue
		}

		// check if the policy match the node
		switch {
		case !nodeMatches(&node, r.Match):
			continue
		case !foundPolicy:
			profilesNames[*r.Profile] = struct{}{}
			priority = *r.Priority
			foundPolicy = true
		case *r.Priority == priority:
			profilesNames[*r.Profile] = struct{}{}
		default:
			break
		}
	}

	return profilesNames
}

func nodeMatches(node *corev1.Node, matchRuleList []ptpv1.MatchRule) bool {
	// loop over Match list
	for _, m := range matchRuleList {

		// nodeName has higher priority than nodeLabel
		// return immediately if nodeName matches
		// make sure m.NodeName pointer is not nil before
		// comparing values
		if m.NodeName != nil && *m.NodeName == node.Name {
			return true
		}

		// return immediately when label matches
		// this makes sure priority field is respected
		for k := range node.Labels {
			// make sure m.NodeLabel pointer is not nil before
			// comparing values
			if m.NodeLabel != nil && *m.NodeLabel == k {
				return true
			}
		}
	}

	return false
}

func returnMapKeys(profiles map[string]interface{}) []string {
	keys := make([]string, len(profiles))

	i := 0
	for k := range profiles {
		keys[i] = k
		i++
	}

	return keys
}
